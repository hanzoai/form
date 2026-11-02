package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hanzoai/form"
)

const token = "tok-never-printed"

// fake is a commerce source in memory.
type fake struct {
	forms []Origin
	rows  map[string][]Row // by "<form>/<kind>"
	stray map[string]int   // records of a kind that name no form
}

func (f *fake) Forms(fn func(Origin) error) error {
	for _, o := range f.forms {
		if err := fn(o); err != nil {
			return err
		}
	}
	return nil
}

func (f *fake) Count(id, kind string) (int, error) {
	if id != "" {
		return len(f.rows[id+"/"+kind]), nil
	}
	n := f.stray[kind]
	for _, o := range f.forms {
		n += len(f.rows[o.ID+"/"+kind])
	}
	return n, nil
}

func (f *fake) Rows(id, kind string, fn func(Row) error) error {
	for _, r := range f.rows[id+"/"+kind] {
		if err := fn(r); err != nil {
			return err
		}
	}
	return nil
}

// many is n records of one kind, emails from mail.
func many(n int, mail func(i int) string) []Row {
	out := make([]Row, n)
	for i := range out {
		out[i] = Row{ID: fmt.Sprintf("r%05d", i), At: time.Date(2015, 3, 4, 12, 0, 0, 0, time.UTC), Email: mail(i), Metadata: map[string]any{"name": fmt.Sprint("n", i)}}
	}
	return out
}

func unique(i int) string { return fmt.Sprintf("u%d@example.com", i) }

// server is /v1/form as the importer uses it: a form stated by PUT, and a batch
// imported keyed by external id, or by address on a subscribe form.
type server struct {
	mu      sync.Mutex
	forms   map[string]form.Form
	held    map[string]bool
	puts    map[string]int
	batches []int
	calls   int
	fail    map[int]int // the n-th import call answers this status
}

func serve(t *testing.T) (*server, *httptest.Server) {
	s := &server{forms: map[string]form.Form{}, held: map[string]bool{}, puts: map[string]int{}, fail: map[int]int{}}
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /v1/form/forms/{slug}", func(w http.ResponseWriter, r *http.Request) {
		var f form.Form
		if err := json.NewDecoder(r.Body).Decode(&f); err != nil || f.ID != r.PathValue("slug") || f.Slug != f.ID {
			http.Error(w, "bad form", 400)
			return
		}
		s.mu.Lock()
		s.forms[f.Slug] = f
		s.puts[f.Slug]++
		s.mu.Unlock()
		_ = json.NewEncoder(w).Encode(f)
	})
	mux.HandleFunc("POST /v1/form/forms/{slug}/import", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.calls++
		if code := s.fail[s.calls]; code != 0 {
			w.Header().Set("Retry-After", "0")
			http.Error(w, "not now", code)
			return
		}
		var in form.ImportIn
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || len(in.Responses) > 500 || in.ID != r.PathValue("slug") {
			http.Error(w, "bad batch", 400)
			return
		}
		f, ok := s.forms[in.ID]
		if !ok {
			http.Error(w, "no form", 404)
			return
		}
		out := form.Imports{Failed: []form.ImportFailure{}}
		for _, im := range in.Responses {
			if im.At == "" {
				out.Failed = append(out.Failed, form.ImportFailure{External: im.External, Reason: "at is an RFC 3339 time"})
				continue
			}
			key := f.Slug + "/" + im.External
			if f.Kind == form.Subscribe {
				key = f.Slug + "/" + im.Email
			}
			if s.held[key] {
				out.Existing++
				continue
			}
			s.held[key] = true
			out.Created++
		}
		s.batches = append(s.batches, len(in.Responses))
		_ = json.NewEncoder(w).Encode(out)
	})
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "sign in", 401)
			return
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(h.Close)
	return s, h
}

func importerFor(src Source, h *httptest.Server, cursor string, out *bytes.Buffer) *importer {
	return &importer{
		src:    src,
		name:   "fake",
		api:    &client{ctx: context.Background(), base: h.URL, token: token, http: h.Client(), pause: time.Millisecond, tries: 4},
		cursor: cursor,
		out:    out,
	}
}

func TestDefine(t *testing.T) {
	o := Origin{ID: "AbC", Name: "News", Type: "subscribe", Forward: "ops@acme.com", Recipient: "Ops", Thanks: "redirect", URL: "https://acme.com/t", Welcome: true}
	f, refs := define(o, "commerce-abc", map[string]bool{"First Name": true, "first_name": true, "Email": true, " ": true})
	if f.ID != "commerce-abc" || f.Slug != "commerce-abc" || f.Kind != form.Subscribe || f.Title != "News" {
		t.Fatalf("form %+v", f)
	}
	want := []form.Field{
		{Ref: "email", Type: form.Email, Title: "Email", Required: true},
		{Ref: "field", Type: form.ShortText, Title: "field"},
		{Ref: "email_2", Type: form.ShortText, Title: "Email"},
		{Ref: "first_name", Type: form.ShortText, Title: "First Name"},
		{Ref: "first_name_2", Type: form.ShortText, Title: "first_name"},
	}
	if !reflect.DeepEqual(f.Fields, want) {
		t.Fatalf("fields %+v", f.Fields)
	}
	if refs["Email"] != "email_2" || refs["first_name"] != "first_name_2" || refs[" "] != "field" {
		t.Fatalf("refs %v", refs)
	}
	s := f.Sinks
	if s.Events || s.Welcome != nil || s.CRM == nil || s.CRM.List != "News" || s.Forward == nil || *s.Forward != (form.Forward{Email: "ops@acme.com", Name: "Ops"}) {
		t.Fatalf("sinks %+v", s)
	}
	if f.Thanks == nil || f.Thanks.Redirect != "https://acme.com/t" {
		t.Fatalf("thanks %+v", f.Thanks)
	}

	p, _ := define(Origin{ID: "x", Name: "Contact", Type: "submit", Thanks: "redirect", URL: "http://acme.com/t"}, "commerce-x", nil)
	if p.Kind != form.Plain || p.Fields[0].Required || p.Thanks != nil || p.Sinks.CRM != nil || p.Sinks.Forward != nil || len(p.Fields) != 1 {
		t.Fatalf("plain %+v %+v", p, p.Sinks)
	}
}

func TestSlug(t *testing.T) {
	seen := map[string]string{}
	if s, err := slugOf("AbC9", seen); err != nil || s != "commerce-abc9" {
		t.Fatalf("slug %q %v", s, err)
	}
	if _, err := slugOf("aBc9", seen); err == nil || !strings.Contains(err.Error(), "both become commerce-abc9") {
		t.Fatalf("a case-only twin: %v", err)
	}
	if _, err := slugOf("a_b", seen); err == nil {
		t.Fatal("an underscore made a slug")
	}
}

func TestImported(t *testing.T) {
	r := Row{ID: "x1", At: time.Date(2015, 3, 4, 4, 0, 0, 0, time.FixedZone("PST", -8*3600)), Email: " Ada@Example.COM ",
		Metadata: map[string]any{"b": 2.0, "a": "hi", "c": nil, "d": map[string]any{"k": true}}}
	got := imported(Subscriber, r, map[string]string{"a": "a", "b": "b", "c": "c", "d": "d"})
	text := func(s string) *string { return &s }
	want := form.Imported{External: "subscriber_x1", At: "2015-03-04T12:00:00Z", Email: "ada@example.com", Source: "commerce",
		Answers: []form.Answer{{Field: "a", Text: text("hi")}, {Field: "b", Text: text("2")}, {Field: "d", Text: text(`{"k":true}`)}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("imported %+v", got)
	}
	if imported(Submission, Row{ID: "y"}, nil).At != "" {
		t.Fatal("a missing time is sent as one")
	}
}

func TestBatchBoundaries(t *testing.T) {
	src := &fake{
		forms: []Origin{{ID: "a", Name: "A", Type: "submit"}, {ID: "b", Name: "B", Type: "subscribe"}, {ID: "c", Name: "C", Type: "submit"}},
		rows:  map[string][]Row{"a/submission": many(1001, unique), "b/subscriber": many(500, unique)},
	}
	s, h := serve(t)
	var out bytes.Buffer
	cursor := filepath.Join(t.TempDir(), "cursor")
	if err := importerFor(src, h, cursor, &out).run(); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !reflect.DeepEqual(s.batches, []int{500, 500, 1, 500}) {
		t.Fatalf("batches %v", s.batches)
	}
	if !reflect.DeepEqual(s.puts, map[string]int{"commerce-a": 1, "commerce-b": 1, "commerce-c": 1}) {
		t.Fatalf("puts %v", s.puts)
	}
	if f := s.forms["commerce-a"]; len(f.Fields) != 2 || f.Fields[1].Ref != "name" {
		t.Fatalf("fields %+v", f.Fields)
	}
	if !strings.Contains(out.String(), "done: forms 3, records 1501, duplicates skipped 0, created 1501, existing 0, refused 0") {
		t.Fatal(out.String())
	}
	if _, err := os.Stat(cursor); !os.IsNotExist(err) {
		t.Fatalf("the cursor outlives the run: %v", err)
	}
}

func TestSubscribeFormsDedupeByAddress(t *testing.T) {
	rows := many(502, unique)
	rows[0].Email, rows[1].Email, rows[501].Email = "a@example.com", " A@Example.com", "a@example.com"
	plain := many(2, func(int) string { return "same@example.com" })
	src := &fake{
		forms: []Origin{{ID: "s", Name: "S", Type: "subscribe"}, {ID: "p", Name: "P", Type: "submit"}},
		rows:  map[string][]Row{"s/subscriber": rows, "p/submission": plain},
	}
	s, h := serve(t)
	var out bytes.Buffer
	if err := importerFor(src, h, filepath.Join(t.TempDir(), "cursor"), &out).run(); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !reflect.DeepEqual(s.batches, []int{500, 1, 2}) {
		t.Fatalf("batches %v", s.batches)
	}
	if !strings.Contains(out.String(), "records 504, duplicates skipped 1, created 502, existing 1, refused 0") {
		t.Fatal(out.String())
	}
}

func TestResumeAfterTheLastAcknowledgedBatch(t *testing.T) {
	src := &fake{
		forms: []Origin{{ID: "z", Name: "Z", Type: "submit"}, {ID: "a", Name: "A", Type: "submit"}, {ID: "b", Name: "B", Type: "submit"}},
		rows: map[string][]Row{
			"z/submission": many(3, unique),
			"a/subscriber": many(5, unique),
			"a/submission": many(1200, unique),
			"b/submission": many(10, unique),
		},
	}
	s, h := serve(t)
	s.fail[4] = 400
	cursor := filepath.Join(t.TempDir(), "cursor")
	var out bytes.Buffer
	if err := importerFor(src, h, cursor, &out).run(); err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("the refused batch: %v", err)
	}
	var m mark
	b, _ := os.ReadFile(cursor)
	if err := json.Unmarshal(b, &m); err != nil || m != (mark{Source: "fake", API: h.URL, Form: "a", Kind: Submission, Row: "r00499"}) {
		t.Fatalf("cursor %s %v", b, err)
	}

	clear(s.fail)
	if err := importerFor(src, h, cursor, &out).run(); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !reflect.DeepEqual(s.batches, []int{3, 5, 500, 500, 200, 10}) {
		t.Fatalf("batches %v", s.batches)
	}
	if !reflect.DeepEqual(s.puts, map[string]int{"commerce-z": 1, "commerce-a": 2, "commerce-b": 1}) {
		t.Fatalf("puts %v", s.puts)
	}
	if strings.Contains(out.String(), "existing 1") || len(s.held) != 1218 {
		t.Fatalf("held %d\n%s", len(s.held), out.String())
	}
}

func TestACursorMadeElsewhereIsRefused(t *testing.T) {
	src := &fake{forms: []Origin{{ID: "a", Name: "A", Type: "submit"}}, rows: map[string][]Row{"a/submission": many(2, unique)}}
	_, h := serve(t)
	cursor := filepath.Join(t.TempDir(), "cursor")
	write := func(m mark) {
		b, _ := json.Marshal(m)
		if err := os.WriteFile(cursor, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	for _, c := range []struct {
		m    mark
		want string
	}{
		{mark{Source: "fake", API: "https://elsewhere", Form: "a", Kind: Submission, Row: "r00000"}, "was written for fake at https://elsewhere"},
		{mark{Source: "fake", API: h.URL, Form: "gone", Kind: Submission, Row: "r00000"}, "names form gone"},
		{mark{Source: "fake", API: h.URL, Form: "a", Kind: Submission, Row: "gone"}, "submission gone is gone from form a"},
	} {
		write(c.m)
		if err := importerFor(src, h, cursor, &out).run(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("cursor %+v: %v", c.m, err)
		}
	}
}

func TestRetriesBackOffOn429And5xx(t *testing.T) {
	src := &fake{forms: []Origin{{ID: "a", Name: "A", Type: "submit"}}, rows: map[string][]Row{"a/submission": many(2, unique)}}
	s, h := serve(t)
	s.fail[1], s.fail[2] = 503, 429
	var out bytes.Buffer
	if err := importerFor(src, h, filepath.Join(t.TempDir(), "cursor"), &out).run(); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if s.calls != 3 || !reflect.DeepEqual(s.batches, []int{2}) {
		t.Fatalf("calls %d batches %v", s.calls, s.batches)
	}

	s, h = serve(t)
	s.fail[1] = 400
	if err := importerFor(src, h, filepath.Join(t.TempDir(), "cursor"), &out).run(); err == nil || s.calls != 1 {
		t.Fatalf("a 400 is retried: %d calls, %v", s.calls, err)
	}

	s, h = serve(t)
	for i := 1; i <= 4; i++ {
		s.fail[i] = 502
	}
	if err := importerFor(src, h, filepath.Join(t.TempDir(), "cursor"), &out).run(); err == nil || s.calls != 4 {
		t.Fatalf("tries are not bounded: %d calls, %v", s.calls, err)
	}
	if strings.Contains(out.String(), token) {
		t.Fatal("the bearer is written")
	}
}

func TestRefusedRecordsAreNamed(t *testing.T) {
	rows := many(3, unique)
	rows[1].At = time.Time{}
	src := &fake{forms: []Origin{{ID: "a", Name: "A", Type: "submit", Welcome: true}}, rows: map[string][]Row{"a/submission": rows}}
	_, h := serve(t)
	var out bytes.Buffer
	err := importerFor(src, h, filepath.Join(t.TempDir(), "cursor"), &out).run()
	if err == nil || !strings.Contains(err.Error(), "1 records refused") {
		t.Fatalf("run: %v", err)
	}
	for _, line := range []string{"commerce-a: refused submission_r00001: at is an RFC 3339 time", "commerce-a: welcome mail not carried"} {
		if !strings.Contains(out.String(), line) {
			t.Errorf("no %q in\n%s", line, out.String())
		}
	}
}

func TestFlags(t *testing.T) {
	dir, _, _ := store(t)
	var out bytes.Buffer
	ctx := context.Background()
	t.Setenv("TOKEN", "")
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"export"}, "usage: form import"},
		{[]string{"import", "-org", "acme"}, "-data and -org are required"},
		{[]string{"import", "-data", dir, "-org", "acme"}, "TOKEN is required"},
		{[]string{"import", "-data", dir, "-org", "acme", "-token", "x", "-api", "http://api.hanzo.ai"}, "-api is an https address"},
	} {
		if err := run(ctx, c.args, &out); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: %v", c.args, err)
		}
	}
	if err := run(ctx, []string{"import", "-data", dir, "-org", "acme", "-dry"}, &out); err != nil || !strings.Contains(out.String(), "total: forms 2") {
		t.Fatalf("dry: %v\n%s", err, out.String())
	}
}
