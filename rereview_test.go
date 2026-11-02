package form

// rereview_test.go — Red's second-round probes, held as tests.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zap-proto/zip"
)

// empties is a JSON array of about n bytes of empty answers, opened by prefix.
func empties(prefix string, n int) []byte {
	var b bytes.Buffer
	b.WriteString(prefix)
	b.WriteString(`[`)
	for b.Len() < n-8 {
		b.WriteString(`{},`)
	}
	b.WriteString(`{}]}`)
	return b.Bytes()
}

// post sends body to path as content type ct.
func post(r *rig, path, ct string, body []byte, hdr ...string) (int, string) {
	req := httptest.NewRequest("POST", path, bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := r.app.Test(req, zip.TestConfig{Timeout: 60 * time.Second, FailOnTimeout: true})
	if err != nil {
		r.t.Fatal(err)
	}
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

var one = map[string]any{"slug": "x", "title": "X", "fields": []any{map[string]any{"ref": "a", "type": "short_text", "title": "A"}}}

// A submission that comes in through MCP, with no route in front of it, is
// judged in the same order: the rate refuses it before a byte is decoded.
func TestEveryDoorIsJudgedBeforeDecoding(t *testing.T) {
	r := mount(t)
	r.do("POST", "/v1/form/forms", "acme", one)
	r.allow = false
	args := empties(`{"id":"acme.x","answers":`, BodyCeiling-256)
	env := append([]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"submit_form_response","arguments":`), args...)
	env = append(env, []byte(`}}`)...)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	var wg sync.WaitGroup
	outs := make(chan string, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, o := post(r, "/mcp", "application/json", env, "X-Test-IP", "203.0.113.9")
			outs <- o
		}()
	}
	wg.Wait()
	close(outs)
	out := <-outs
	runtime.ReadMemStats(&after)
	if !strings.Contains(out, "too many submissions") {
		t.Fatalf("an MCP submission past the rate: %.200s", out)
	}
	if grew := (after.TotalAlloc - before.TotalAlloc) >> 20; grew > 400 {
		t.Fatalf("eight 1 MiB MCP submissions past the rate allocated %d MiB", grew)
	}
	if n := r.count("acme", Responses); n != 0 {
		t.Fatalf("a refused submission was filed: %d", n)
	}
}

// A refusal names at most a few problems, however many answers a body holds.
func TestARefusalIsShort(t *testing.T) {
	r := mount(t)
	r.do("POST", "/v1/form/forms", "acme", one)
	body := []byte(`{"answers":[` + strings.Repeat(`{"field":"nope","text":"x"},`, 60) + `{"field":"nope","text":"x"}]}`)
	code, out := post(r, "/v1/form/public/acme.x/responses", "application/json", body)
	if code != http.StatusBadRequest || len(out) > 4096 {
		t.Fatalf("refusal %d, %d bytes", code, len(out))
	}
	big := empties(`{"answers":`, 60<<10)
	if code, out := post(r, "/v1/form/public/acme.x/responses", "application/json", big); code != http.StatusBadRequest || len(out) > 4096 {
		t.Fatalf("60 KiB of answers: %d, %d bytes", code, len(out))
	}
}

// A response deleted while its filing waits on a retry is filed nowhere and is
// not written back.
func TestADeletedResponseStaysDeleted(t *testing.T) {
	held := schedule
	schedule = []time.Duration{0, 200 * time.Millisecond, 200 * time.Millisecond, 200 * time.Millisecond, 200 * time.Millisecond}
	t.Cleanup(func() { schedule = held })
	r := mount(t)
	r.do("POST", "/v1/form/forms", "acme", one)
	r.mu.Lock()
	r.refuse = true
	r.mu.Unlock()
	_, body := r.do("POST", "/v1/form/public/acme.x/responses", "", `{"answers":[{"field":"a","text":"erase me"}]}`)
	var rc Receipt
	_ = json.Unmarshal([]byte(body), &rc)
	time.Sleep(50 * time.Millisecond)
	if code, _ := r.do("DELETE", "/v1/form/forms/x/responses/"+rc.ID, "acme", nil); code != http.StatusNoContent {
		t.Fatalf("delete: %d", code)
	}
	r.mu.Lock()
	r.refuse = false
	r.mu.Unlock()
	time.Sleep(time.Second)
	r.mu.Lock()
	filed := len(r.filed)
	r.mu.Unlock()
	if filed != 0 || r.count("acme", Responses) != 0 {
		t.Fatalf("a deleted response was filed %d times, %d records back", filed, r.count("acme", Responses))
	}
}

// However many orgs fill the shared pool, another org still has its reserve.
func TestNoSetOfOrgsFillsTheQueue(t *testing.T) {
	d := newDeliverer(&service{})
	for o := range 11 {
		org := "noisy" + string(rune('a'+o))
		for range queuedOrg {
			d.take(org)
		}
	}
	for i := range reserve {
		if _, ok := d.take("quiet"); !ok {
			t.Fatalf("a quiet org was refused its reserve at %d", i)
		}
	}
	if _, ok := d.take("quiet"); ok {
		t.Fatal("a quiet org went past its reserve into a full pool")
	}
}

// A mail budget a document leaves out is the default, never none.
func TestAnAbsentMailBudgetIsTheDefault(t *testing.T) {
	var st Settings
	if err := json.Unmarshal([]byte(`{"limits":{"fields":100,"hidden":10,"body":65536,"rate":30,"window":3600,"webhooks":10}}`), &st); err != nil {
		t.Fatal(err)
	}
	st.Types = Defaults().Types
	clean, p := st.Check()
	if len(p) != 0 || clean.Limits.Forward != Defaults().Limits.Forward || clean.Limits.Welcome != Defaults().Limits.Welcome {
		t.Fatalf("checked %+v, problems %v", clean.Limits, p)
	}
	s := &service{d: Deps{Settings: func(context.Context) Settings { return st }}}
	if got := s.settings(context.Background()).Limits; got.Forward == 0 || got.Welcome == 0 {
		t.Fatalf("in force %+v", got)
	}
}

// A webhook names a person only when the respondent signed in.
func TestAWebhookNamesNoClaimedPerson(t *testing.T) {
	f := Form{ID: "f", PublicID: "acme.f", Title: "F"}
	claim := payload(f, Response{Respondent: Respondent{Kind: "email", Email: "ada@example.com", PersonID: "p1"}}, "e1")
	if claim.FormResponse.PersonID != "" || claim.FormResponse.Verified {
		t.Fatalf("a claim posted as %+v", claim.FormResponse)
	}
	signed := payload(f, Response{Respondent: Respondent{Kind: "account", PersonID: "p1", Verified: true}}, "e2")
	if signed.FormResponse.PersonID != "p1" || !signed.FormResponse.Verified {
		t.Fatalf("a signed-in respondent posted as %+v", signed.FormResponse)
	}
}
