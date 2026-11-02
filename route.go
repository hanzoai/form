package form

// route.go — a completed response, out to the form's sinks: the CRM, the
// forwarding address, the welcome mail and the webhooks. The response is
// already stored when this runs, so a sink that fails costs that sink and is
// said in the log, never the response.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// sinkBudget bounds one response's trip through its sinks.
const sinkBudget = 30 * time.Second

// routing bounds the responses whose sinks run detached at once. Past it a
// response's sinks run on the submitting request, which is the backpressure.
var routing = make(chan struct{}, 64)

// route sends a completed response to the form's sinks.
func (s *service) route(org string, f Form, r Response, ip string) {
	run := func() {
		ctx, cancel := context.WithTimeout(context.Background(), sinkBudget)
		defer cancel()
		defer func() {
			if p := recover(); p != nil {
				s.d.Log.Error("form: routing a response panicked", "org", org, "form", f.ID, "err", p)
			}
		}()
		s.sink(ctx, org, f, r, ip)
	}
	select {
	case routing <- struct{}{}:
		go func() {
			defer func() { <-routing }()
			run()
		}()
	default:
		run()
	}
}

// sink is route's work, in order.
func (s *service) sink(ctx context.Context, org string, f Form, r Response, ip string) {
	sinks := f.Sinks
	if sinks == nil {
		return
	}
	if sinks.CRM != nil && s.d.File != nil {
		s.file(ctx, org, f, r, 0)
	}
	if fw := sinks.Forward; fw != nil && s.d.Mail != nil {
		m := Mail{To: fw.Email, Name: fw.Name, Kind: "forward", Form: f.ID,
			Subject: clip("New response: "+f.Title, maxSubject), Text: s.letter(f, r)}
		if err := s.d.Mail(ctx, org, m); err != nil {
			s.d.Log.Warn("form: a response was not forwarded", "org", org, "form", f.ID, "err", err)
		}
	}
	// A welcome goes to a signed-in respondent alone. A claimed address is asked
	// the list's double opt-in instead: it is mailed a link, and only a press on it
	// joins the list and records consent.
	verified := r.Respondent.Verified
	if w := sinks.Welcome; w != nil && s.d.Mail != nil && verified && s.first(ctx, org, f.ID, r) {
		m := Mail{To: r.Respondent.Email, Kind: "welcome", Form: f.ID, Subject: w.Subject, Text: w.Body}
		if err := s.d.Mail(ctx, org, m); err != nil {
			s.d.Log.Warn("form: a welcome mail did not go out", "org", org, "form", f.ID, "err", err)
		}
	}
	if c := sinks.CRM; c != nil && c.List != "" && !verified && r.Respondent.Email != "" && s.d.Confirm != nil {
		ask := Confirmation{Email: r.Respondent.Email, List: c.List, Form: f.ID, Public: f.PublicID, IP: ip}
		if err := s.d.Confirm(ctx, org, ask); err != nil {
			s.d.Log.Warn("form: a claimed address was not asked to confirm", "org", org, "form", f.ID, "err", err)
		}
	}
	s.out.respond(ctx, org, f, r)
}

// refiling counts the filings waiting on a retry, in all and per org. Past
// either bound a failed filing waits for POST .../file, its reason on the
// response, so no org's outage holds another's retries.
var refiling = struct {
	sync.Mutex
	all  int
	orgs map[string]int
}{orgs: map[string]int{}}

// Bounds on the filings waiting on a retry in this process.
const (
	refileMost = 1000
	refileOrg  = 100
)

// hold takes a retry place for org, and reports whether there was one.
func hold(org string) bool {
	refiling.Lock()
	defer refiling.Unlock()
	if refiling.all >= refileMost || refiling.orgs[org] >= refileOrg {
		return false
	}
	refiling.all++
	refiling.orgs[org]++
	return true
}

// release returns a retry place taken for org.
func release(org string) {
	refiling.Lock()
	defer refiling.Unlock()
	refiling.all--
	if refiling.orgs[org]--; refiling.orgs[org] <= 0 {
		delete(refiling.orgs, org)
	}
}

// file files a completed response on the CRM and records on the response
// whether it did. A filing that failed is tried again on the delivery schedule;
// one a restart abandoned stays unfiled with its reason, and POST .../file files
// it now.
func (s *service) file(ctx context.Context, org string, f Form, r Response, n int) error {
	// A response deleted since it was completed is filed nowhere and not written
	// again: what was erased stays erased.
	if _, gerr := s.d.Store.Get(ctx, org, Responses, r.ID); errors.Is(gerr, ErrNotFound) {
		return nil
	}
	err := s.d.File(ctx, org, s.filing(f, r))
	rec := Record{"id": r.ID, "filed": err == nil, "filing": ""}
	if err != nil {
		s.d.Log.Warn("form: a response was not filed in the CRM", "org", org, "form", f.ID, "response", r.ID, "err", err)
		rec["filing"] = clip("not filed: "+err.Error(), 500)
	}
	if _, gerr := s.d.Store.Get(ctx, org, Responses, r.ID); errors.Is(gerr, ErrNotFound) {
		return err
	}
	if perr := s.d.Store.Put(ctx, org, Responses, rec); perr != nil {
		s.d.Log.Warn("form: whether a response was filed could not be recorded", "org", org, "response", r.ID, "err", perr)
	}
	if err == nil || n+1 >= len(schedule) {
		return err
	}
	if !hold(org) {
		return err
	}
	time.AfterFunc(schedule[n+1], func() {
		defer release(org)
		c, cancel := context.WithTimeout(context.Background(), sinkBudget)
		defer cancel()
		_ = s.file(c, org, f, r, n+1)
	})
	return err
}

// first reports whether r is the first completed response its address made to
// the form: a welcome is mailed once.
func (s *service) first(ctx context.Context, org, form string, r Response) bool {
	// The earliest of the address's completed responses is the first, whichever
	// of two that complete together is routed first.
	got, err := s.d.Store.List(ctx, org, Responses, Query{
		Where:   and(leaf("form", "is", form), leaf("email", "is", r.Respondent.Email), leaf("completed", "is", true)),
		Sort:    "at,id",
		PerPage: 1,
	})
	if err != nil {
		s.d.Log.Warn("form: whether an address answered before could not be read; no welcome is mailed", "org", org, "form", form, "err", err)
		return false
	}
	return len(got.Items) == 1 && field(got.Items[0], "id") == r.ID
}

// filing is a completed response as the CRM files it. The answer that named the
// person's address is left out: the person holds it, and the timeline carries
// the person, never the address.
func (s *service) filing(f Form, r Response) Filing {
	c := f.Sinks.CRM
	email := r.Respondent.Email
	skip := ""
	if r.Respondent.Kind == "email" {
		skip = emailRef(f)
	}
	var qa []QA
	for _, a := range r.Answers {
		fd, ok := fieldOf(f, a.Field)
		if !ok || fd.Ref == skip {
			continue
		}
		qa = append(qa, QA{Question: fd.Title, Answer: said(fd, a)})
	}
	return Filing{
		Ref: "form:" + r.ID, Form: f.ID, Public: f.PublicID, Title: f.Title, Kind: f.Kind,
		PersonID: r.Respondent.PersonID, Email: email, Verified: r.Respondent.Verified,
		Name: textOf(f, r.Answers, c.Name), Company: textOf(f, r.Answers, c.Company), List: c.List,
		Answers: qa, Hidden: r.Hidden, Visitor: r.Respondent.Visitor, Subject: r.Respondent.Subject,
		Path: r.Path, At: when(r.SubmittedAt),
	}
}

// letter is a response as the forwarding mail's plain text: each question and
// what was said, the hidden values, and who it is from.
func (s *service) letter(f Form, r Response) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s received a response.\n\n", f.Title)
	for _, a := range r.Answers {
		fd, ok := fieldOf(f, a.Field)
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "%s\n%s\n\n", fd.Title, said(fd, a))
	}
	for name, v := range r.Hidden {
		fmt.Fprintf(&b, "%s: %s\n", name, v)
	}
	if e := r.Respondent.Email; e != "" {
		fmt.Fprintf(&b, "From: %s\n", e)
	}
	fmt.Fprintf(&b, "Response: %s\n", r.ID)
	return b.String()
}

// emit states an occurrence on the event stream, when the host has one.
func (s *service) emit(ctx context.Context, org string, e Event) {
	if s.d.Emit != nil {
		s.d.Emit(ctx, org, e)
	}
}

// expiring is the forms whose responses are being expired, so a burst of
// submissions runs one sweep per form.
var expiring = make(chan struct{}, 4)

// expirePage bounds how many responses one sweep deletes.
const expirePage = 200

// expire deletes the form's responses past the platform's retention, detached
// and a page at a time.
func (s *service) expire(org, form string, st Settings) {
	if st.Retention <= 0 {
		return
	}
	select {
	case expiring <- struct{}{}:
	default:
		return
	}
	go func() {
		defer func() { <-expiring }()
		ctx, cancel := context.WithTimeout(context.Background(), sinkBudget)
		defer cancel()
		cut := s.d.Now().Add(-time.Duration(st.Retention) * 24 * time.Hour)
		got, err := s.d.Store.List(ctx, org, Responses, Query{
			// Measured from when the record was stored, never from when it says it
			// was made: an import keeps its original time and is kept like the rest.
			Where: and(leaf("form", "is", form), leaf("created", "lt", baseTime(cut))), PerPage: expirePage,
		})
		if err != nil {
			s.d.Log.Warn("form: expired responses could not be read", "org", org, "form", form, "err", err)
			return
		}
		var ids []string
		for _, r := range got.Items {
			ids = append(ids, field(r, "id"))
		}
		if len(ids) == 0 {
			return
		}
		if err := s.d.Store.Delete(ctx, org, Responses, ids...); err != nil {
			s.d.Log.Warn("form: expired responses could not be deleted", "org", org, "form", form, "err", err)
		}
	}()
}
