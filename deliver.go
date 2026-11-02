package form

// deliver.go — posting a completed response to each of its form's webhooks:
// signed, bounded, retried on a schedule, and every attempt recorded on the
// delivery.
//
// THE RETRIES LIVE IN THIS PROCESS. An attempt that fails is tried again after
// 30 seconds, 2 minutes, 10 minutes and an hour; the fifth failure is final. A
// restart drops the retries it was waiting on, and the delivery says so: its
// status stays retrying with its next attempt in the past, and redeliver sends it.
//
// ONLY THE PUBLIC INTERNET IS POSTED TO. The client dials an address only after
// checking it is not loopback, private, link-local, multicast or unspecified,
// on the address actually dialled, so a name that resolves inward is refused at
// connect and not trusted at check. It follows no redirect and reads at most 4
// KiB of an answer.

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// schedule is when each attempt is made, after the response was completed.
var schedule = []time.Duration{0, 30 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour}

// The states of a delivery.
const (
	delivered = "delivered"
	retrying  = "retrying"
	failedOut = "failed"
)

// attemptBudget bounds one post.
const attemptBudget = 10 * time.Second

// Bounds on deliveries, so no org's receivers hold another org's posts and no
// set of orgs fills the queue for the rest.
const (
	// perOrg is how many posts one org has in flight at once.
	perOrg = 4
	// queuedOrg is how many deliveries one org has waiting, on a retry or a slot.
	queuedOrg = 200
	// queuedAll is the shared pool: deliveries past an org's reserve take a
	// place in it, and when it is full only reserves are left.
	queuedAll = 2000
	// reserve is what every org may have waiting whatever the pool holds.
	reserve = 5
	// queuedMost is the process's hard bound on everything waiting.
	queuedMost = 4000
)

// deliverer posts responses to webhooks: each org through its own few slots, and
// a bounded number waiting. A delivery past either bound is recorded failed with
// the reason, and retry sends it.
type deliverer struct {
	s    *service
	mu   sync.Mutex
	orgs map[string]*lane
	// all is everything waiting; shared what of it is past its org's reserve.
	all, shared int
}

// lane is one org's slots and how many of its deliveries wait.
type lane struct {
	slots  chan struct{}
	queued int
	// pooled is how many of queued hold a place in the shared pool.
	pooled int
}

func newDeliverer(s *service) *deliverer {
	return &deliverer{s: s, orgs: map[string]*lane{}}
}

// respond posts a completed response to every enabled webhook of its form.
func (d *deliverer) respond(ctx context.Context, org string, f Form, r Response) {
	got, err := d.s.d.Store.List(ctx, org, Webhooks, Query{Where: and(leaf("form", "is", f.ID), leaf("enabled", "is", true)), PerPage: 100})
	if err != nil {
		d.s.d.Log.Warn("form: the form's webhooks could not be read; its response is posted nowhere", "org", org, "form", f.ID, "err", err)
		return
	}
	for _, h := range got.Items {
		w := webhookOf(h)
		dl := Delivery{ID: derived("delivery", w.ID+"\x00"+r.ID), Webhook: w.ID, Response: r.ID, Status: retrying}
		d.later(ctx, org, f, r, w, dl, 0)
	}
}

// take holds a place in org's queue, and reports whether there was one.
func (d *deliverer) take(org string) (*lane, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	l := d.orgs[org]
	if l == nil {
		l = &lane{slots: make(chan struct{}, perOrg)}
		d.orgs[org] = l
	}
	switch {
	case d.all >= queuedMost, l.queued >= queuedOrg:
		return nil, false
	case l.queued >= reserve && d.shared >= queuedAll:
		return nil, false
	}
	if l.queued >= reserve {
		d.shared++
		l.pooled++
	}
	l.queued++
	d.all++
	return l, true
}

// give returns a place taken in org's queue.
func (d *deliverer) give(org string, l *lane) {
	d.mu.Lock()
	defer d.mu.Unlock()
	l.queued--
	d.all--
	if l.pooled > 0 {
		l.pooled--
		d.shared--
	}
	if l.queued == 0 {
		delete(d.orgs, org)
	}
}

// errQueued is a delivery a full queue did not take.
var errQueued = errors.New("the delivery queue was full; retry it")

// later makes attempt n of a delivery when its time comes, in its org's lane.
func (d *deliverer) later(ctx context.Context, org string, f Form, r Response, w Webhook, dl Delivery, n int) {
	if n >= len(schedule) {
		return
	}
	l, ok := d.take(org)
	if !ok {
		d.record(ctx, org, f, r, w, dl, 0, errQueued, false)
		return
	}
	time.AfterFunc(schedule[n], func() {
		l.slots <- struct{}{}
		ctx, cancel := context.WithTimeout(context.Background(), 2*attemptBudget)
		got := d.attempt(ctx, org, f, r, w, dl, true)
		cancel()
		<-l.slots
		d.give(org, l)
		if got.Status == retrying {
			c, cancel := context.WithTimeout(context.Background(), attemptBudget)
			d.later(c, org, f, r, w, got, n+1)
			cancel()
		}
	})
}

// attempt posts once and records how it went. scheduled is whether this is one
// of the schedule's attempts, which decides what a failure leaves to come.
func (d *deliverer) attempt(ctx context.Context, org string, f Form, r Response, w Webhook, dl Delivery, scheduled bool) Delivery {
	dl.Attempts++
	code, err := d.post(ctx, org, f, r, w, dl.ID)
	return d.record(ctx, org, f, r, w, dl, code, err, scheduled)
}

// record writes how a delivery went: delivered, retrying with its next attempt,
// or failed with why, in words that name no address the service dialled.
func (d *deliverer) record(ctx context.Context, org string, f Form, r Response, w Webhook, dl Delivery, code int, err error, scheduled bool) Delivery {
	s := d.s
	dl.Code, dl.Error, dl.Next = code, "", ""
	switch {
	case err == nil:
		dl.Status = delivered
	case scheduled && dl.Attempts < len(schedule):
		dl.Status = retrying
		dl.Error = err.Error()
		dl.Next = stamp(s.d.Now().Add(schedule[dl.Attempts]))
	default:
		dl.Status = failedOut
		dl.Error = err.Error()
	}
	rec := Record{"id": dl.ID, "webhook": w.ID, "form": f.ID, "response": r.ID, "status": dl.Status, "attempts": dl.Attempts, "code": dl.Code, "error": dl.Error, "next": ""}
	if dl.Next != "" {
		rec["next"] = baseTime(when(dl.Next))
	}
	if err := s.d.Store.Put(ctx, org, Deliveries, rec); err != nil {
		s.d.Log.Warn("form: a delivery was made and could not be recorded", "org", org, "webhook", w.ID, "err", err)
	}
	if held, err := s.d.Store.Get(ctx, org, Deliveries, dl.ID); err == nil {
		dl = deliveryOf(held)
	}
	return dl
}

// errBlocked is an address the dialler refused, one not on the public internet. It
// reads exactly as an address that did not resolve or answer, so the log is no
// oracle for what a name resolves to.
var errBlocked = errors.New("the address could not be reached")

// failure is a post's error as the delivery log says it: what kind of failure,
// never the address dialled or what a name resolved to.
func failure(err error) error {
	var ne net.Error
	switch {
	case errors.Is(err, errBlocked):
		return errBlocked
	case errors.As(err, &ne) && ne.Timeout(), errors.Is(err, context.DeadlineExceeded):
		return errors.New("the address did not answer in time")
	}
	return errors.New("the address could not be reached")
}

// Replay is how long a receiver should take a delivery's signature: one whose
// timestamp is older than this, or a delivery id already seen, is a replay.
const Replay = 5 * time.Minute

// post signs and sends the response, and answers the status the address gave.
func (d *deliverer) post(ctx context.Context, org string, f Form, r Response, w Webhook, event string) (int, error) {
	s := d.s
	if s.d.Secrets == nil {
		return 0, errors.New("this deployment keeps no secrets")
	}
	secret, err := s.d.Secrets.Get(ctx, org, secretName(w.ID))
	if err != nil {
		s.d.Log.Warn("form: a webhook's secret could not be read", "org", org, "webhook", w.ID, "err", err)
		return 0, errors.New("the webhook's secret could not be read")
	}
	body, err := json.Marshal(payload(f, r, event))
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, attemptBudget)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(body))
	if err != nil {
		return 0, errors.New("the address is not one a delivery can be made to")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Hanzo-Form/1")
	req.Header.Set("Hanzo-Delivery", event)
	req.Header.Set("Hanzo-Signature", Sign(secret, s.d.Now().Unix(), body))
	resp, err := s.d.HTTP.Do(req)
	if err != nil {
		return 0, failure(err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.StatusCode, fmt.Errorf("the address answered %d", resp.StatusCode)
	}
	return resp.StatusCode, nil
}

// Sign is a delivery's Hanzo-Signature header: t=<unix seconds>,sha256=<base64
// of HMAC-SHA256 under the webhook's secret of the seconds, a dot and the raw
// body>. A receiver recomputes it over the body it read, compares in constant
// time, and refuses a timestamp older than Replay and an event_id it has seen.
func Sign(secret []byte, t int64, body []byte) string {
	ts := strconv.FormatInt(t, 10)
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(ts + "."))
	m.Write(body)
	return "t=" + ts + ",sha256=" + base64.StdEncoding.EncodeToString(m.Sum(nil))
}

// Posted is the body of a delivery.
type Posted struct {
	// EventID is the delivery's id: a receiver that has seen it has this response.
	EventID string `json:"event_id"`
	// EventType is form_response.
	EventType string `json:"event_type"`
	// FormResponse is the response and the form it answers.
	FormResponse PostedResponse `json:"form_response"`
}

// PostedResponse is a response as a delivery carries it.
type PostedResponse struct {
	FormID      string            `json:"form_id"`
	PublicID    string            `json:"public_id"`
	Token       string            `json:"token"`
	LandedAt    string            `json:"landed_at,omitempty"`
	SubmittedAt string            `json:"submitted_at"`
	Hidden      map[string]string `json:"hidden,omitempty"`
	Definition  PostedDefinition  `json:"definition"`
	Answers     []Answer          `json:"answers"`
	// PersonID is the CRM person the respondent is, set only when Verified.
	PersonID string `json:"person_id,omitempty"`
	// Verified is whether the respondent signed in: an address typed into the
	// form is a claim, and names nobody here.
	Verified bool `json:"verified"`
}

// PostedDefinition is the form as a delivery names it.
type PostedDefinition struct {
	ID     string        `json:"id"`
	Title  string        `json:"title"`
	Fields []PostedField `json:"fields"`
}

// PostedField is a field as a delivery names it.
type PostedField struct {
	ID    string `json:"id"`
	Ref   string `json:"ref"`
	Type  string `json:"type"`
	Title string `json:"title"`
}

func payload(f Form, r Response, event string) Posted {
	def := PostedDefinition{ID: f.ID, Title: f.Title, Fields: []PostedField{}}
	for _, fd := range f.Fields {
		def.Fields = append(def.Fields, PostedField{ID: fd.ID, Ref: fd.Ref, Type: fd.Type, Title: fd.Title})
	}
	return Posted{EventID: event, EventType: "form_response", FormResponse: PostedResponse{
		FormID: f.ID, PublicID: f.PublicID, Token: r.Token, LandedAt: r.LandedAt, SubmittedAt: r.SubmittedAt,
		Hidden: r.Hidden, Definition: def, Answers: r.Answers, Verified: r.Respondent.Verified, PersonID: verifiedPerson(r.Respondent),
	}}
}

// verifiedPerson is the respondent's person when they are verified, else none.
func verifiedPerson(r Respondent) string {
	if r.Verified {
		return r.PersonID
	}
	return ""
}

// deliveryOf is a stored delivery as the wire carries it.
func deliveryOf(r Record) Delivery {
	d := Delivery{ID: field(r, "id"), Webhook: field(r, "webhook"), Response: field(r, "response"), Status: field(r, "status"),
		Error: field(r, "error"), Next: wire(r, "next"), Created: wire(r, "created"), Updated: wire(r, "updated")}
	if v, ok := r["attempts"].(float64); ok {
		d.Attempts = int(v)
	}
	if v, ok := r["code"].(float64); ok {
		d.Code = int(v)
	}
	return d
}

// guarded is the client deliveries are posted with: public addresses only, no
// redirects, bounded.
func guarded() *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, Control: public}
	return &http.Client{
		Timeout: attemptBudget,
		Transport: &http.Transport{
			DialContext:            dialer.DialContext,
			ForceAttemptHTTP2:      true,
			TLSHandshakeTimeout:    5 * time.Second,
			ResponseHeaderTimeout:  attemptBudget,
			MaxResponseHeaderBytes: 16 << 10,
			MaxIdleConns:           16,
			IdleConnTimeout:        30 * time.Second,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// public refuses a connection to any address that is not on the public internet.
func public(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return errBlocked
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !Public(ip) {
		return errBlocked
	}
	return nil
}

// reserved are the ranges no webhook lives in, beside what netip names: shared
// and carrier NAT, the IPv4 "this network", protocol and benchmarking blocks and
// the reserved class E, and the IPv6 transition ranges that carry an IPv4
// address inside them (NAT64, 6to4, Teredo), any of which reaches a private one.
var reserved = func() []netip.Prefix {
	var out []netip.Prefix
	for _, p := range []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "198.18.0.0/15", "240.0.0.0/4",
		"64:ff9b::/96", "64:ff9b:1::/48", "2002::/16", "2001::/32",
	} {
		out = append(out, netip.MustParsePrefix(p))
	}
	return out
}()

// Public reports whether ip is an address on the public internet.
func Public(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	for _, p := range reserved {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}
