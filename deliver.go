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

// deliverer posts responses to webhooks, a bounded number at once.
type deliverer struct {
	s     *service
	slots chan struct{}
}

func newDeliverer(s *service) *deliverer {
	return &deliverer{s: s, slots: make(chan struct{}, 16)}
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
		d.later(org, f, r, w, dl, 0)
	}
}

// later makes attempt n of a delivery when its time comes.
func (d *deliverer) later(org string, f Form, r Response, w Webhook, dl Delivery, n int) {
	if n >= len(schedule) {
		return
	}
	time.AfterFunc(schedule[n], func() {
		d.slots <- struct{}{}
		defer func() { <-d.slots }()
		ctx, cancel := context.WithTimeout(context.Background(), 2*attemptBudget)
		defer cancel()
		got := d.attempt(ctx, org, f, r, w, dl, true)
		if got.Status == retrying {
			d.later(org, f, r, w, got, n+1)
		}
	})
}

// attempt posts once and records how it went. scheduled is whether this is one
// of the schedule's attempts, which decides what a failure leaves to come.
func (d *deliverer) attempt(ctx context.Context, org string, f Form, r Response, w Webhook, dl Delivery, scheduled bool) Delivery {
	s := d.s
	dl.Attempts++
	code, err := d.post(ctx, org, f, r, w, dl.ID)
	dl.Code, dl.Error, dl.Next = code, "", ""
	switch {
	case err == nil:
		dl.Status = delivered
	case scheduled && dl.Attempts < len(schedule):
		dl.Status = retrying
		dl.Error = clip(err.Error(), 500)
		dl.Next = stamp(s.d.Now().Add(schedule[dl.Attempts]))
	default:
		dl.Status = failedOut
		dl.Error = clip(err.Error(), 500)
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

// post signs and sends the response, and answers the status the address gave.
func (d *deliverer) post(ctx context.Context, org string, f Form, r Response, w Webhook, event string) (int, error) {
	s := d.s
	if s.d.Secrets == nil {
		return 0, errors.New("this deployment keeps no secrets")
	}
	secret, err := s.d.Secrets.Get(ctx, org, secretName(w.ID))
	if err != nil {
		return 0, fmt.Errorf("the webhook's secret could not be read: %w", err)
	}
	body, err := json.Marshal(payload(f, r, event))
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, attemptBudget)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Hanzo-Form/1")
	req.Header.Set("Hanzo-Delivery", event)
	req.Header.Set("Hanzo-Signature", Sign(secret, body))
	resp, err := s.d.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.StatusCode, fmt.Errorf("the address answered %d", resp.StatusCode)
	}
	return resp.StatusCode, nil
}

// Sign is a delivery's Hanzo-Signature header: sha256= and the base64 of the
// HMAC-SHA256 of the body under the webhook's secret. A receiver computes the
// same over the raw body it read and compares in constant time.
func Sign(secret, body []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	return "sha256=" + base64.StdEncoding.EncodeToString(m.Sum(nil))
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
	PersonID    string            `json:"person_id,omitempty"`
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
		Hidden: r.Hidden, Definition: def, Answers: r.Answers, PersonID: r.Respondent.PersonID,
	}}
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
		return err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return err
	}
	if !Public(ip) {
		return fmt.Errorf("%s is not a public address", ip)
	}
	return nil
}

// cgnat is the carrier-grade NAT range, shared address space no webhook lives in.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// Public reports whether ip is an address on the public internet.
func Public(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsValid() && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() &&
		!ip.IsMulticast() && !ip.IsUnspecified() && !cgnat.Contains(ip)
}
