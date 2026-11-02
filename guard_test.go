package form

// guard_test.go — what Red's probes found, held as tests: each one fails if the
// finding comes back.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zap-proto/zip"
)

// A body past the ceiling is refused before one of its answers is allocated.
func TestABodyPastTheBoundIsNeverDecoded(t *testing.T) {
	r := mount(t)
	r.do("POST", "/v1/form/forms", "hanzo", video)
	var b bytes.Buffer
	b.WriteString(`{"answers":[`)
	for b.Len() < (16<<20)-16 {
		b.WriteString(`{},`)
	}
	b.WriteString(`{}]}`)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	req := httptest.NewRequest("POST", "/v1/form/public/hanzo.video-feedback/responses", bytes.NewReader(b.Bytes()))
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.app.Test(req, zip.TestConfig{Timeout: 60 * time.Second, FailOnTimeout: true})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	runtime.ReadMemStats(&after)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("a 16 MiB body: %d", resp.StatusCode)
	}
	if grew := (after.TotalAlloc - before.TotalAlloc) >> 20; grew > 256 {
		t.Fatalf("refusing a 16 MiB body allocated %d MiB", grew)
	}
	var in Submission
	if err := in.UnmarshalJSON(b.Bytes()); err != nil || in.Answers != nil || in.size != b.Len() {
		t.Fatalf("a body past the ceiling was decoded: %d answers", len(in.Answers))
	}
}

// Only JSON is a submission: a cross-site form or a no-cors fetch cannot send it
// without asking first.
func TestASubmissionIsJSON(t *testing.T) {
	r := mount(t)
	r.do("POST", "/v1/form/forms", "hanzo", video)
	for _, ct := range []string{"text/plain", "application/x-www-form-urlencoded", ""} {
		req := httptest.NewRequest("POST", "/v1/form/public/hanzo.video-feedback/responses", strings.NewReader(`{"answers":[{"field":"rating","number":5}]}`))
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		resp, err := r.app.Test(req, zip.TestConfig{Timeout: 10 * time.Second, FailOnTimeout: true})
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusUnsupportedMediaType {
			t.Errorf("%q: %d", ct, resp.StatusCode)
		}
	}
	if n := r.count("hanzo", Responses); n != 0 {
		t.Fatalf("a non-JSON body was filed: %d", n)
	}
}

// An address the host cannot read is one shared budget, never none.
func TestNoAddressIsStillCounted(t *testing.T) {
	r := mount(t)
	r.do("POST", "/v1/form/forms", "hanzo", video)
	r.allow = false
	if code, _ := r.do("POST", "/v1/form/public/hanzo.video-feedback/responses", "", map[string]any{"answers": []any{map[string]any{"field": "rating", "number": 5}}}); code != http.StatusTooManyRequests {
		t.Fatalf("no address past the budget: %d", code)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.ips) == 0 || r.ips[len(r.ips)-1] != "" {
		t.Fatalf("the rate was not asked for an empty address: %v", r.ips)
	}
}

// A number is bounded on the way in, and the summary stays finite whatever was stored.
func TestANumberIsBoundedAndTheSummaryFinite(t *testing.T) {
	r := mount(t)
	form := map[string]any{"slug": "n", "title": "N", "fields": []any{map[string]any{"ref": "x", "type": "number", "title": "X"}}}
	r.do("POST", "/v1/form/forms", "hanzo", form)
	if code, _ := r.do("POST", "/v1/form/public/hanzo.n/responses", "", `{"answers":[{"field":"x","number":1.7e308}]}`); code != http.StatusBadRequest {
		t.Fatalf("1.7e308: %d", code)
	}
	id := derived("form", "n")
	_ = r.store.Put(context.Background(), "hanzo", Responses,
		Record{"id": "aaaaaaaaaaaaaaa", "form": id, "completed": true, "answers": []any{map[string]any{"field": "x", "number": 1.7e308}}, "at": baseTime(time.Now())},
		Record{"id": "bbbbbbbbbbbbbbb", "form": id, "completed": true, "answers": []any{map[string]any{"field": "x", "number": 1.7e308}}, "at": baseTime(time.Now())})
	code, body := r.do("GET", "/v1/form/forms/n/summary", "hanzo", nil)
	if code != 200 || !strings.Contains(body, `"answered":2`) {
		t.Fatalf("summary: %d %s", code, body)
	}
}

// A form cannot take a slug another form is: a slug is set when a form is made.
func TestASlugIsNeverAdopted(t *testing.T) {
	r := mount(t)
	r.do("POST", "/v1/form/forms", "hanzo", video)
	code, body := r.do("POST", "/v1/form/forms", "hanzo", map[string]any{"title": "Other", "fields": []any{map[string]any{"ref": "a", "type": "short_text", "title": "A"}}})
	if code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	var other Form
	_ = json.Unmarshal([]byte(body), &other)
	put := map[string]any{"slug": "video-feedback", "title": "Other", "fields": []any{map[string]any{"ref": "a", "type": "short_text", "title": "A"}}}
	if code, _ := r.do("PUT", "/v1/form/forms/"+other.ID, "hanzo", put); code != http.StatusBadRequest {
		t.Fatalf("replace with another form's slug: %d", code)
	}
	if code, body := r.do("GET", "/v1/form/public/hanzo.video-feedback", "", nil); code != 200 || !strings.Contains(body, "How was the video?") {
		t.Fatalf("the slug's own form: %d %s", code, body)
	}
}

// The delivery log says what kind of failure it was, never the address dialled.
func TestTheDeliveryLogNamesNoAddress(t *testing.T) {
	r := mount(t, func(d *Deps) { d.HTTP = nil })
	r.do("POST", "/v1/form/forms", "hanzo", video)
	code, body := r.do("POST", "/v1/form/forms/video-feedback/webhooks", "hanzo", map[string]any{"url": "https://127.0.0.1:9/x"})
	if code != 201 {
		t.Fatalf("webhook: %d %s", code, body)
	}
	r.do("POST", "/v1/form/public/hanzo.video-feedback/responses", "", map[string]any{"answers": []any{map[string]any{"field": "rating", "number": 5}}})
	var said string
	r.wait(func() bool {
		r.store.mu.Lock()
		defer r.store.mu.Unlock()
		for _, d := range r.store.table("hanzo", Deliveries) {
			said, _ = d["error"].(string)
		}
		return said != ""
	})
	if said != "the address could not be reached" {
		t.Fatalf("the log says %q", said)
	}
}

// Retention counts from when a record was stored: imported history is kept.
func TestRetentionCountsFromStoring(t *testing.T) {
	r := mount(t, func(d *Deps) {
		d.Settings = func(context.Context) Settings { st := Defaults(); st.Retention = 30; return st }
	})
	r.do("POST", "/v1/form/forms", "hanzo", video)
	code, body := r.do("POST", "/v1/form/forms/video-feedback/import", "hanzo", map[string]any{"responses": []any{
		map[string]any{"external": "old_1", "at": "2014-09-29T00:00:00Z", "answers": []any{map[string]any{"field": "rating", "number": 5}}},
	}})
	if code != 200 || !strings.Contains(body, `"created":1`) {
		t.Fatalf("import: %d %s", code, body)
	}
	r.do("POST", "/v1/form/public/hanzo.video-feedback/responses", "", map[string]any{"answers": []any{map[string]any{"field": "rating", "number": 4}}})
	time.Sleep(100 * time.Millisecond)
	if n := r.count("hanzo", Responses); n != 2 {
		t.Fatalf("retention took imported history: %d left", n)
	}
}

// One org's deliveries cannot fill the queue another org's wait in.
func TestOneOrgCannotFillAnothersQueue(t *testing.T) {
	d := newDeliverer(&service{})
	for range queuedOrg {
		if _, ok := d.take("noisy"); !ok {
			t.Fatal("an org was refused before its bound")
		}
	}
	if _, ok := d.take("noisy"); ok {
		t.Fatal("an org went past its bound")
	}
	l, ok := d.take("quiet")
	if !ok || cap(l.slots) != perOrg {
		t.Fatal("a quiet org found no room")
	}
}

// Format characters are dropped and an address is ASCII.
func TestFormatCharactersAreDropped(t *testing.T) {
	if got, ok := line("Refund approved ‮decnalac‬ ​", 200); !ok || strings.ContainsAny(got, "‮‬​") {
		t.Fatalf("line kept %q", got)
	}
	if address("аdmin@hanzo.ai") {
		t.Fatal("a Cyrillic letter passed for a Latin one")
	}
}

// The CSV's header is no formula either.
func TestTheCSVHeaderIsNeverAFormula(t *testing.T) {
	r := mount(t)
	r.do("POST", "/v1/form/forms", "hanzo", map[string]any{"slug": "x", "title": "X", "fields": []any{map[string]any{"ref": "a", "type": "short_text", "title": "=HYPERLINK(\"https://evil.example/\",\"Open\")"}}})
	r.do("POST", "/v1/form/public/hanzo.x/responses", "", `{"answers":[{"field":"a","text":"-2+3"}]}`)
	_, out := r.do("GET", "/v1/form/forms/x/export?format=csv", "hanzo", nil)
	var e Export
	_ = json.Unmarshal([]byte(out), &e)
	if !strings.Contains(e.CSV, `'=HYPERLINK`) || !strings.Contains(e.CSV, `'-2+3`) {
		t.Fatalf("csv:\n%s", e.CSV)
	}
}

// A filing the CRM refused is kept on the response with why, and filed later.
func TestAFailedFilingIsKeptAndFiledLater(t *testing.T) {
	r := mount(t)
	r.do("POST", "/v1/form/forms", "hanzo", video)
	r.mu.Lock()
	r.refuse = true
	r.mu.Unlock()
	_, body := r.do("POST", "/v1/form/public/hanzo.video-feedback/responses", "", map[string]any{"answers": []any{map[string]any{"field": "rating", "number": 5}}})
	var got Receipt
	_ = json.Unmarshal([]byte(body), &got)
	var listed string
	r.wait(func() bool {
		_, listed = r.do("GET", "/v1/form/forms/video-feedback/responses?filed=false", "hanzo", nil)
		return strings.Contains(listed, "the CRM is down")
	})
	r.mu.Lock()
	r.refuse = false
	r.mu.Unlock()
	code, out := r.do("POST", "/v1/form/forms/video-feedback/responses/"+got.ID+"/file", "hanzo", nil)
	if code != 200 || !strings.Contains(out, `"filed":true`) || strings.Contains(out, "the CRM is down") {
		t.Fatalf("refile: %d %s", code, out)
	}
}

// The platform's default sinks mail nobody: an address is each form's own.
func TestDefaultSinksNameNoAddress(t *testing.T) {
	st := Defaults()
	st.Sinks.Forward = &Forward{Email: "sales@example.com"}
	if _, p := st.Check(); len(p) != 1 || !strings.Contains(p[0], "forward") {
		t.Fatalf("problems %v", p)
	}
}
