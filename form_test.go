package form

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zap-proto/zip"
)

type callerKey struct{}
type clientKey struct{}

// rig is the service mounted the way a host mounts it, with every sink recorded.
type rig struct {
	t      *testing.T
	app    *zip.App
	store  *memory
	mu     sync.Mutex
	events []Event
	filed  []Filing
	mails  []Mail
	secret map[string][]byte
	allow  bool
	// asked are the double opt-ins asked; ips the addresses the rate was asked
	// about; refuse makes the CRM refuse filings while it is set.
	asked  []Confirmation
	ips    []string
	refuse bool
}

func (r *rig) Put(_ context.Context, org, name string, v []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.secret[org+"/"+name] = v
	return nil
}

func (r *rig) Get(_ context.Context, org, name string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.secret[org+"/"+name]
	if !ok {
		return nil, ErrNoSecret
	}
	return v, nil
}

func (r *rig) Delete(_ context.Context, org, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.secret, org+"/"+name)
	return nil
}

func mount(t *testing.T, edit ...func(*Deps)) *rig {
	t.Helper()
	r := &rig{t: t, store: newMemory(), secret: map[string][]byte{}, allow: true}
	d := Deps{
		Store:    r.store,
		Platform: "admin",
		Caller: func(ctx context.Context) Caller {
			c, _ := ctx.Value(callerKey{}).(Caller)
			return c
		},
		Client: func(ctx context.Context) Client {
			c, _ := ctx.Value(clientKey{}).(Client)
			return c
		},
		Allow: func(_ context.Context, _, ip string, _ int, _ time.Duration) bool {
			r.mu.Lock()
			r.ips = append(r.ips, ip)
			r.mu.Unlock()
			return r.allow
		},
		Settings: func(context.Context) Settings { return Defaults() },
		Person:   func(email string) string { return derived("person", email) },
		Emit: func(_ context.Context, _ string, e Event) {
			r.mu.Lock()
			r.events = append(r.events, e)
			r.mu.Unlock()
		},
		File: func(_ context.Context, _ string, f Filing) error {
			r.mu.Lock()
			defer r.mu.Unlock()
			if r.refuse {
				return errors.New("the CRM is down")
			}
			r.filed = append(r.filed, f)
			return nil
		},
		Confirm: func(_ context.Context, _ string, c Confirmation) error {
			r.mu.Lock()
			r.asked = append(r.asked, c)
			r.mu.Unlock()
			return nil
		},
		Mail: func(_ context.Context, _ string, m Mail) error {
			r.mu.Lock()
			r.mails = append(r.mails, m)
			r.mu.Unlock()
			return nil
		},
		Secrets: r,
		HTTP:    http.DefaultClient,
	}
	for _, e := range edit {
		e(&d)
	}
	// The host reads a body as cloud's edge does: up to 16 MiB.
	host := zip.New(zip.Config{BodyLimit: 16 << 20})
	host.Use(zip.H(func(c *zip.Ctx) error {
		who := Caller{Org: c.Header("X-Test-Org"), Admin: c.Header("X-Test-Admin") == "true", Subject: c.Header("X-Test-Subject"), Email: c.Header("X-Test-Email")}
		cl := Client{IP: c.Header("X-Test-IP"), OptedOut: c.Header("Sec-GPC") == "1"}
		c.SetContext(context.WithValue(context.WithValue(c.Context(), callerKey{}, who), clientKey{}, cl))
		return c.Next()
	}))
	host.Use(New(d))
	r.app = host
	return r
}

// do sends a request; as is the org an admin acts in, "" for nobody.
func (r *rig) do(method, path, as string, body any, hdr ...string) (int, string) {
	r.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		if s, ok := body.(string); ok {
			b = []byte(s)
		}
		rd = strings.NewReader(string(b))
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	if as != "" {
		req.Header.Set("X-Test-Org", as)
		req.Header.Set("X-Test-Admin", "true")
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := r.app.Test(req, zip.TestConfig{Timeout: 10 * time.Second, FailOnTimeout: true})
	if err != nil {
		r.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// count is how many records org's collection holds.
func (r *rig) count(org, collection string) int {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	return len(r.store.table(org, collection))
}

func (r *rig) wait(cond func() bool) {
	r.t.Helper()
	for i := 0; i < 200; i++ {
		r.mu.Lock()
		ok := cond()
		r.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.t.Fatal("timed out waiting for a sink")
}

var video = map[string]any{
	"slug": "video-feedback", "title": "How was the video?",
	"fields": []any{
		map[string]any{"ref": "rating", "type": "rating", "title": "Rate the video", "required": true},
		map[string]any{"ref": "comment", "type": "long_text", "title": "Anything to tell us?"},
		map[string]any{"ref": "email", "type": "email", "title": "Your email"},
	},
	"hidden": []any{"video"},
}

func TestAFormIsMadeReadAndPublished(t *testing.T) {
	r := mount(t)
	code, body := r.do("POST", "/v1/form/forms", "hanzo", video)
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, body)
	}
	var f Form
	_ = json.Unmarshal([]byte(body), &f)
	if f.ID != derived("form", "video-feedback") || f.PublicID != "hanzo.video-feedback" || f.Kind != Plain {
		t.Fatalf("made %+v", f)
	}
	if f.Settings.Visibility != "public" || f.Settings.Mode != "one" || f.Sinks == nil || !f.Sinks.Events || f.Sinks.CRM == nil {
		t.Fatalf("defaults not filled: %+v", f)
	}
	if f.Fields[0].ID == "" || f.Fields[0].Steps != 5 {
		t.Fatalf("field not normalized: %+v", f.Fields[0])
	}
	if code, body = r.do("POST", "/v1/form/forms", "hanzo", video); code != http.StatusConflict {
		t.Fatalf("a second form with one slug: %d %s", code, body)
	}
	if code, body = r.do("GET", "/v1/form/forms/video-feedback", "hanzo", nil); code != 200 || !strings.Contains(body, `"public_id":"hanzo.video-feedback"`) {
		t.Fatalf("get by slug: %d %s", code, body)
	}
	code, body = r.do("GET", "/v1/form/public/hanzo.video-feedback", "", nil)
	if code != 200 || strings.Contains(body, "sinks") || strings.Contains(body, "visibility") {
		t.Fatalf("public form: %d %s", code, body)
	}
	if code, _ = r.do("GET", "/v1/form/public/nowhere.video-feedback", "", nil); code != 404 {
		t.Fatalf("an unknown org's form: %d", code)
	}
	if r.store.installed["nowhere\x00"+Forms] {
		t.Fatal("a public read of an unknown org reached into a store for it")
	}
}

func TestOnlyAnOrgAdminManagesForms(t *testing.T) {
	r := mount(t)
	if code, _ := r.do("POST", "/v1/form/forms", "", video); code != http.StatusUnauthorized {
		t.Fatalf("anonymous create: %d", code)
	}
	if code, _ := r.do("POST", "/v1/form/forms", "", video, "X-Test-Org", "hanzo"); code != http.StatusForbidden {
		t.Fatalf("member create: %d", code)
	}
}

func TestASubmissionIsOneResponseAndReachesItsSinks(t *testing.T) {
	r := mount(t)
	r.do("POST", "/v1/form/forms", "hanzo", video)
	sub := map[string]any{
		"token": "r_9f1c2a7b", "answers": []any{map[string]any{"field": "rating", "number": 5}, map[string]any{"field": "comment", "text": "Loved it"}},
		"hidden": map[string]any{"video": "7mVTEAHHjPc"}, "visitor": "v_k3j2h1", "landed_at": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339),
	}
	code, body := r.do("POST", "/v1/form/public/hanzo.video-feedback/responses", "", sub)
	if code != http.StatusCreated {
		t.Fatalf("submit: %d %s", code, body)
	}
	var first Receipt
	_ = json.Unmarshal([]byte(body), &first)
	code, body = r.do("POST", "/v1/form/public/hanzo.video-feedback/responses", "", sub)
	var again Receipt
	_ = json.Unmarshal([]byte(body), &again)
	if code != http.StatusCreated || again.ID != first.ID || !again.Completed {
		t.Fatalf("a second click: %d %s", code, body)
	}
	if n := r.count("hanzo", Responses); n != 1 {
		t.Fatalf("two clicks held %d responses", n)
	}
	r.wait(func() bool { return len(r.filed) == 1 })
	time.Sleep(50 * time.Millisecond)
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.filed) != 1 {
		t.Fatalf("filed %d times", len(r.filed))
	}
	f := r.filed[0]
	if f.PersonID != "" || f.Visitor != "v_k3j2h1" || f.Ref != "form:"+first.ID || f.Title != "How was the video?" || f.Hidden["video"] != "7mVTEAHHjPc" {
		t.Fatalf("an anonymous response is the org's own timeline entry: %+v", f)
	}
	var names []string
	for _, e := range r.events {
		names = append(names, e.Name)
		if e.Response != "" && strings.Contains(strings.Join(e.Fields, ","), "Loved") {
			t.Fatalf("an event carries what was said: %+v", e)
		}
	}
	if strings.Join(names, ",") != "form_started,form_submitted" {
		t.Fatalf("events %v", names)
	}
}

func TestTheSignedInPersonIsTheTokensNotTheBodys(t *testing.T) {
	r := mount(t)
	r.do("POST", "/v1/form/forms", "hanzo", video)
	sub := map[string]any{"token": "r_signed01", "answers": []any{
		map[string]any{"field": "rating", "number": 4}, map[string]any{"field": "email", "text": "mallory@example.com"},
	}}
	code, body := r.do("POST", "/v1/form/public/hanzo.video-feedback/responses", "", sub,
		"X-Test-Subject", "4f1c0e8a", "X-Test-Email", "Ada@Example.com")
	if code != http.StatusCreated {
		t.Fatalf("submit: %d %s", code, body)
	}
	r.wait(func() bool { return len(r.filed) == 1 })
	r.mu.Lock()
	f := r.filed[0]
	r.mu.Unlock()
	if f.Email != "ada@example.com" || f.Subject != "4f1c0e8a" || f.PersonID != derived("person", "ada@example.com") {
		t.Fatalf("the person is the token's: %+v", f)
	}
	for _, e := range r.events {
		if e.PersonID != derived("person", "ada@example.com") {
			t.Fatalf("an event is not keyed by the person: %+v", e)
		}
	}
}

func TestATypedAddressIsAClaim(t *testing.T) {
	r := mount(t)
	r.do("POST", "/v1/form/forms", "hanzo", video)
	r.do("POST", "/v1/form/public/hanzo.video-feedback/responses", "", map[string]any{"answers": []any{
		map[string]any{"field": "rating", "number": 3}, map[string]any{"field": "email", "text": "ada@example.com"},
	}})
	r.wait(func() bool { return len(r.filed) == 1 })
	r.mu.Lock()
	defer r.mu.Unlock()
	f := r.filed[0]
	if f.Verified || f.PersonID != derived("person", "ada@example.com") {
		t.Fatalf("a typed address is filed as an unverified claim: %+v", f)
	}
	for _, qa := range f.Answers {
		if strings.Contains(qa.Answer, "ada@") {
			t.Fatalf("the timeline carries the address: %+v", f.Answers)
		}
	}
	for _, e := range r.events {
		if e.PersonID != "" {
			t.Fatalf("a claim is somebody on the event stream: %+v", e)
		}
	}
}

func TestAVerifiedFormTakesNoClaim(t *testing.T) {
	r := mount(t)
	form := map[string]any{}
	for k, v := range video {
		form[k] = v
	}
	form["settings"] = map[string]any{"identity": "verified"}
	if code, body := r.do("POST", "/v1/form/forms", "hanzo", form); code != 201 || !strings.Contains(body, `"identity":"verified"`) {
		t.Fatalf("create: %d %s", code, body)
	}
	r.do("POST", "/v1/form/public/hanzo.video-feedback/responses", "", map[string]any{"visitor": "v_1", "answers": []any{
		map[string]any{"field": "rating", "number": 3}, map[string]any{"field": "email", "text": "ada@example.com"},
	}})
	r.wait(func() bool { return len(r.filed) == 1 })
	r.mu.Lock()
	defer r.mu.Unlock()
	if f := r.filed[0]; f.PersonID != "" || f.Email != "" || f.Visitor != "v_1" {
		t.Fatalf("a verified-only form filed a typed address: %+v", f)
	}
}

func TestASubmissionIsReadAgainstItsForm(t *testing.T) {
	r := mount(t)
	r.do("POST", "/v1/form/forms", "hanzo", video)
	for name, sub := range map[string]map[string]any{
		"unknown field":  {"answers": []any{map[string]any{"field": "rating", "number": 5}, map[string]any{"field": "nope", "text": "x"}}},
		"rating range":   {"answers": []any{map[string]any{"field": "rating", "number": 9}}},
		"rating as text": {"answers": []any{map[string]any{"field": "rating", "text": "5"}}},
		"required":       {"answers": []any{map[string]any{"field": "comment", "text": "hi"}}},
		"hidden":         {"answers": []any{map[string]any{"field": "rating", "number": 5}}, "hidden": map[string]any{"plan": "x"}},
		"bad email":      {"answers": []any{map[string]any{"field": "rating", "number": 5}, map[string]any{"field": "email", "text": "a@b"}}},
		"honeypot":       {"answers": []any{map[string]any{"field": "rating", "number": 5}}, "website": "http://spam"},
		"twice":          {"answers": []any{map[string]any{"field": "rating", "number": 5}, map[string]any{"field": "rating", "number": 4}}},
	} {
		if code, body := r.do("POST", "/v1/form/public/hanzo.video-feedback/responses", "", sub); code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, code, body)
		}
	}
	if n := r.count("hanzo", Responses); n != 0 {
		t.Fatalf("a refused submission held %d responses", n)
	}
	big := `{"answers":[{"field":"comment","text":"` + strings.Repeat("x", 70<<10) + `"}]}`
	if code, _ := r.do("POST", "/v1/form/public/hanzo.video-feedback/responses", "", big); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("a body past the bound: %d", code)
	}
	r.allow = false
	if code, _ := r.do("POST", "/v1/form/public/hanzo.video-feedback/responses", "", map[string]any{}, "X-Test-IP", "203.0.113.9"); code != http.StatusTooManyRequests {
		t.Fatalf("past the rate: %d", code)
	}
}

func TestAPartialResponseIsCompletedUnderItsToken(t *testing.T) {
	r := mount(t)
	r.do("POST", "/v1/form/forms", "hanzo", video)
	part := map[string]any{"token": "r_partial1", "completed": false, "answers": []any{map[string]any{"field": "rating", "number": 2}}}
	if code, body := r.do("POST", "/v1/form/public/hanzo.video-feedback/responses", "", part); code != 201 || !strings.Contains(body, `"completed":false`) {
		t.Fatalf("partial: %d %s", code, body)
	}
	time.Sleep(30 * time.Millisecond)
	if len(r.filed) != 0 {
		t.Fatal("an open response was filed")
	}
	whole := map[string]any{"token": "r_partial1", "answers": []any{map[string]any{"field": "rating", "number": 2}, map[string]any{"field": "comment", "text": "meh"}}}
	if code, body := r.do("POST", "/v1/form/public/hanzo.video-feedback/responses", "", whole); code != 201 || !strings.Contains(body, `"completed":true`) {
		t.Fatalf("complete: %d %s", code, body)
	}
	r.wait(func() bool { return len(r.filed) == 1 })
	code, body := r.do("GET", "/v1/form/forms/video-feedback/summary", "hanzo", nil)
	if code != 200 || !strings.Contains(body, `"starts":1`) || !strings.Contains(body, `"completions":1`) {
		t.Fatalf("summary: %d %s", code, body)
	}
}

func TestALandingThePageMadeUpIsNone(t *testing.T) {
	r := mount(t)
	r.do("POST", "/v1/form/forms", "hanzo", video)
	for _, at := range []string{"2001-01-01T00:00:00Z", time.Now().Add(time.Hour).UTC().Format(time.RFC3339)} {
		code, body := r.do("POST", "/v1/form/public/hanzo.video-feedback/responses", "", map[string]any{"landed_at": at, "answers": []any{map[string]any{"field": "rating", "number": 5}}})
		if code != 201 {
			t.Fatalf("submit: %d %s", code, body)
		}
	}
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	for _, rec := range r.store.table("hanzo", Responses) {
		if rec["landed"] != nil || rec["duration"] != nil {
			t.Fatalf("a made-up landing is kept: %v", rec)
		}
	}
}

func TestAPrivateFormIsNotServed(t *testing.T) {
	r := mount(t)
	r.do("POST", "/v1/form/forms", "hanzo", video)
	if code, body := r.do("PATCH", "/v1/form/forms/video-feedback", "hanzo", map[string]any{"settings": map[string]any{"visibility": "private"}}); code != 200 {
		t.Fatalf("patch: %d %s", code, body)
	}
	if code, _ := r.do("GET", "/v1/form/public/hanzo.video-feedback", "", nil); code != 404 {
		t.Fatalf("a private form is served: %d", code)
	}
}

func TestASubscribeClaimIsConfirmedNeverJoined(t *testing.T) {
	r := mount(t)
	list := map[string]any{"slug": "news", "kind": "subscribe", "title": "News", "fields": []any{
		map[string]any{"ref": "email", "type": "email", "title": "Email", "required": true},
		map[string]any{"ref": "name", "type": "short_text", "title": "Name"},
	}, "sinks": map[string]any{"crm": map[string]any{"list": "Newsletter", "name": "name"}, "welcome": map[string]any{"subject": "Welcome", "body": "Thanks for joining."}}}
	if code, body := r.do("POST", "/v1/form/forms", "hanzo", list); code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	// A squatter types the address first, then its owner submits: both are kept.
	for _, name := range []string{"Mallory", "Ada"} {
		if code, body := r.do("POST", "/v1/form/public/hanzo.news/responses", "", map[string]any{"answers": []any{
			map[string]any{"field": "email", "text": "ada@example.com"}, map[string]any{"field": "name", "text": name},
		}}); code != 201 {
			t.Fatalf("subscribe: %d %s", code, body)
		}
	}
	if n := r.count("hanzo", Responses); n != 2 {
		t.Fatalf("a later submission from one address was not recorded: %d responses", n)
	}
	r.wait(func() bool { return len(r.asked) == 2 && len(r.filed) == 2 })
	time.Sleep(30 * time.Millisecond)
	r.mu.Lock()
	if len(r.mails) != 0 || r.filed[0].Verified || r.asked[0].List != "Newsletter" || r.asked[0].Email != "ada@example.com" {
		t.Fatalf("a claim was mailed or joined: mails %+v filed %+v asked %+v", r.mails, r.filed, r.asked)
	}
	r.mu.Unlock()

	// Signed in, the person is welcomed once and asked nothing.
	for range 2 {
		if code, body := r.do("POST", "/v1/form/public/hanzo.news/responses", "", map[string]any{"answers": []any{
			map[string]any{"field": "email", "text": "ada@example.com"},
		}}, "X-Test-Subject", "4f1c0e8a", "X-Test-Email", "ada@example.com"); code != 201 {
			t.Fatalf("signed in: %d %s", code, body)
		}
	}
	r.wait(func() bool { return len(r.mails) == 1 })
	time.Sleep(50 * time.Millisecond)
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.mails) != 1 || r.mails[0].Kind != "welcome" || len(r.asked) != 2 {
		t.Fatalf("mails %+v asked %+v", r.mails, r.asked)
	}
}

func TestAWebhookIsSignedAndRecorded(t *testing.T) {
	var got struct {
		sig  string
		body []byte
	}
	done := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		got.sig = req.Header.Get("Hanzo-Signature")
		got.body, _ = io.ReadAll(req.Body)
		done <- struct{}{}
	}))
	defer srv.Close()
	r := mount(t, func(d *Deps) { d.HTTP = srv.Client() })
	r.do("POST", "/v1/form/forms", "hanzo", video)
	if code, _ := r.do("POST", "/v1/form/forms/video-feedback/webhooks", "hanzo", map[string]any{"url": "http://insecure.example"}); code != 400 {
		t.Fatalf("an http address: %d", code)
	}
	code, body := r.do("POST", "/v1/form/forms/video-feedback/webhooks", "hanzo", map[string]any{"url": "https://hooks.example.com/x", "secret": "0123456789abcdef"})
	if code != 201 || !strings.Contains(body, `"secret":"0123456789abcdef"`) {
		t.Fatalf("create webhook: %d %s", code, body)
	}
	var w Webhook
	_ = json.Unmarshal([]byte(body), &w)
	if code, body := r.do("GET", "/v1/form/forms/video-feedback/webhooks/"+w.ID, "hanzo", nil); code != 200 || strings.Contains(body, "secret") {
		t.Fatalf("a read gives the secret back: %d %s", code, body)
	}
	// The test server is not https, so the stored address is pointed at it.
	r.store.mu.Lock()
	r.store.table("hanzo", Webhooks)[w.ID]["url"] = srv.URL
	r.store.mu.Unlock()
	r.do("POST", "/v1/form/public/hanzo.video-feedback/responses", "", map[string]any{"answers": []any{map[string]any{"field": "rating", "number": 5}}})
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("no delivery")
	}
	ts, _, _ := strings.Cut(strings.TrimPrefix(got.sig, "t="), ",")
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || time.Since(time.Unix(sec, 0)) > Replay || got.sig != Sign([]byte("0123456789abcdef"), sec, got.body) {
		t.Fatalf("signature %q does not sign the timestamp and the body", got.sig)
	}
	var p Posted
	_ = json.Unmarshal(got.body, &p)
	if p.EventType != "form_response" || p.FormResponse.PublicID != "hanzo.video-feedback" || len(p.FormResponse.Answers) != 1 {
		t.Fatalf("body %s", got.body)
	}
	r.wait(func() bool {
		r.store.mu.Lock()
		defer r.store.mu.Unlock()
		for _, x := range r.store.table("hanzo", Deliveries) {
			return x["status"] == delivered
		}
		return false
	})
}

func TestOnlyThePublicInternetIsPosted(t *testing.T) {
	for _, a := range []string{"127.0.0.1", "10.1.2.3", "192.168.1.1", "169.254.169.254", "100.64.0.1", "::1", "fd00::1", "0.0.0.0", "::ffff:127.0.0.1",
		"64:ff9b::a00:1", "64:ff9b:1::1", "2002:a00:1::1", "2001:0:4136:e378::1", "0.1.2.3", "192.0.0.8", "198.18.0.1", "240.0.0.1"} {
		if Public(netip.MustParseAddr(a)) {
			t.Errorf("%s is posted to", a)
		}
	}
	if !Public(netip.MustParseAddr("8.8.8.8")) {
		t.Error("a public address is refused")
	}
}

func TestAnImportIsResumable(t *testing.T) {
	r := mount(t)
	r.do("POST", "/v1/form/forms", "hanzo", map[string]any{"slug": "news", "kind": "subscribe", "title": "News", "fields": []any{
		map[string]any{"ref": "email", "type": "email", "title": "Email", "required": true},
	}})
	batch := map[string]any{"responses": []any{
		map[string]any{"external": "sub_1", "at": "2017-03-04T12:00:00Z", "email": "ada@example.com", "source": "commerce"},
		map[string]any{"external": "sub_2", "at": "2017-03-05T12:00:00Z", "email": "Ada@Example.com", "source": "commerce"},
		map[string]any{"external": "sub_3", "at": "nope", "email": "bob@example.com"},
	}, "file": true}
	code, body := r.do("POST", "/v1/form/forms/news/import", "hanzo", batch)
	if code != 200 || !strings.Contains(body, `"created":1`) || !strings.Contains(body, `"existing":1`) || !strings.Contains(body, "sub_3") {
		t.Fatalf("import: %d %s", code, body)
	}
	code, body = r.do("POST", "/v1/form/forms/news/import", "hanzo", batch)
	if code != 200 || !strings.Contains(body, `"created":0`) || !strings.Contains(body, `"existing":2`) {
		t.Fatalf("import again: %d %s", code, body)
	}
	code, body = r.do("GET", "/v1/form/forms/news/responses", "hanzo", nil)
	if code != 200 || !strings.Contains(body, `"created":"2017-03-04T12:00:00Z"`) {
		t.Fatalf("the made time is kept: %d %s", code, body)
	}
}

func TestAnExportCellIsNeverAFormula(t *testing.T) {
	r := mount(t)
	r.do("POST", "/v1/form/forms", "hanzo", video)
	r.do("POST", "/v1/form/public/hanzo.video-feedback/responses", "", map[string]any{"answers": []any{
		map[string]any{"field": "rating", "number": 1}, map[string]any{"field": "comment", "text": "=HYPERLINK(\"x\")"},
	}})
	code, body := r.do("GET", "/v1/form/forms/video-feedback/export?format=csv", "hanzo", nil)
	if code != 200 || !strings.Contains(body, `'=HYPERLINK`) {
		t.Fatalf("export: %d %s", code, body)
	}
}

func TestSettingsAreCheckedWhole(t *testing.T) {
	st := Defaults()
	if _, p := st.Check(); len(p) != 0 {
		t.Fatalf("the defaults fail their own check: %v", p)
	}
	st.Types = st.Types[1:]
	st.Limits.Fields = 0
	if _, p := st.Check(); len(p) != 2 {
		t.Fatalf("want two problems, got %v", p)
	}
}
