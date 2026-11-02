package form

// public.go — the form as a respondent sees it, and answering it. No credential
// is needed for either: the form's public id names its org, the index says
// whether it is published, and a stranger's submission is held to the platform's
// body bound, its rate per address, a field a person never fills, and the form's
// own fields.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/zap-proto/zip"
)

// PublicRef names a form by its public id.
type PublicRef struct {
	// ID is the form's public id, "<org>.<slug or id>", from the path.
	ID string `json:"id"`
}

// PublicForm is a form as a respondent's page draws it: nothing about where its
// responses go.
type PublicForm struct {
	// ID is the form's public id.
	ID string `json:"id"`
	// Kind is form or subscribe.
	Kind string `json:"kind"`
	// Title is the form's title.
	Title string `json:"title"`
	// Fields are the questions, in order.
	Fields []Field `json:"fields"`
	// Welcome is the screen before the first question.
	Welcome *Screen `json:"welcome,omitempty"`
	// Thanks is the screen after a submission, or where the respondent is sent.
	Thanks *Screen `json:"thanks,omitempty"`
	// Hidden names the values the page passes in.
	Hidden []string `json:"hidden,omitempty"`
	// Settings are how the form is answered.
	Settings Drawn `json:"settings"`
	// Theme is the theme the form is drawn in, when it names one.
	Theme *Theme `json:"theme,omitempty"`
}

// Drawn are the settings a renderer reads.
type Drawn struct {
	// Language is the form's language tag.
	Language string `json:"language"`
	// Progress is whether a progress bar is shown.
	Progress bool `json:"progress"`
	// Mode is one (a question at a time) or all.
	Mode string `json:"mode"`
}

// Submission is one respondent's answers to a public form.
type Submission struct {
	size      int
	malformed bool

	// ID is the form's public id, from the path.
	ID string `json:"id"`
	// Token is the client's idempotency token for this response: 8 to 64
	// letters, digits, dashes and underscores. A submission sent twice under one
	// token is one response, so a double click or a retry files nothing twice.
	// Absent, the response is new each time.
	Token string `json:"token"`
	// Answers are the answers, one per field at most.
	Answers []Answer `json:"answers"`
	// Hidden are the values the page passes in, by the names the form declares;
	// each one line of at most 500 characters.
	Hidden map[string]string `json:"hidden"`
	// LandedAt is when the respondent opened the form, RFC 3339.
	LandedAt string `json:"landed_at"`
	// Completed is false to save the answers so far and keep the response open
	// (a later submission under the same token completes it); true or absent
	// submits it whole, every required field answered. A completed response is
	// final.
	Completed *bool `json:"completed"`
	// Visitor is the page's visitor id: letters, digits, dot, dash and
	// underscore, at most 64. It is who the response is from when nobody is
	// signed in and no email field is answered.
	Visitor string `json:"visitor"`
	// Path is the page the form was answered on, at most 500 characters.
	Path string `json:"path"`
	// Website is a field no person sees and no person fills: a submission that
	// fills it is refused.
	Website string `json:"website"`
}

// UnmarshalJSON records the body's size and whether it decoded, and refuses
// nothing: the op judges, in its order.
func (in *Submission) UnmarshalJSON(b []byte) error {
	type body Submission
	var v body
	malformed := json.Unmarshal(b, &v) != nil
	*in = Submission(v)
	in.size, in.malformed = len(b), malformed
	return nil
}

// Receipt is what a submission is answered.
type Receipt struct {
	// ID is the response's id.
	ID string `json:"id"`
	// Completed is whether the response is now submitted whole.
	Completed bool `json:"completed"`
	// SubmittedAt is when it was completed, RFC 3339.
	SubmittedAt string `json:"submitted_at,omitempty"`
	// Thanks is the form's thank-you screen, for a completed response.
	Thanks *Screen `json:"thanks,omitempty"`
}

var (
	tokenShape   = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)
	visitorShape = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
)

// resolve reads the form a public id names, as published. A public id the index
// does not hold, or a form that is private, is a 404.
func (s *service) resolve(ctx context.Context, pid string) (string, *Form, error) {
	pid = strings.TrimSpace(pid)
	if pid == "" || len(pid) > 200 {
		return "", nil, zip.ErrNotFound("form: no such form")
	}
	if err := s.indexReady(ctx); err != nil {
		return "", nil, err
	}
	idx, err := s.d.Store.Get(ctx, s.d.Platform, Index, derived("public", pid))
	if errors.Is(err, ErrNotFound) {
		return "", nil, zip.ErrNotFound("form: no such form")
	}
	if err != nil {
		return "", nil, s.failed("the form could not be found", s.d.Platform, err)
	}
	org, id := field(idx, "org"), field(idx, "form")
	r, err := s.d.Store.Get(ctx, org, Forms, id)
	if errors.Is(err, ErrNotFound) {
		return "", nil, zip.ErrNotFound("form: no such form")
	}
	if err != nil {
		return "", nil, s.failed("the form could not be read", org, err)
	}
	f := formOf(org, r)
	if f.Settings.Visibility != "public" || f.PublicID != pid {
		return "", nil, zip.ErrNotFound("form: no such form")
	}
	return org, &f, nil
}

// Reads a published form as its page draws it: the questions, the screens, the
// hidden values it takes, how it is answered and its theme. Nothing about where
// its responses go. No credential is needed; a form that is private, deleted or
// never was is a 404.
func (s *service) publicForm(ctx context.Context, in *PublicRef) (*PublicForm, error) {
	org, f, err := s.resolve(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	out := &PublicForm{
		ID: f.PublicID, Kind: f.Kind, Title: f.Title, Fields: f.Fields, Welcome: f.Welcome, Thanks: f.Thanks, Hidden: f.Hidden,
		Settings: Drawn{Language: f.Settings.Language, Progress: f.Settings.Progress, Mode: f.Settings.Mode},
	}
	if f.Theme != "" {
		if r, err := s.d.Store.Get(ctx, org, Themes, f.Theme); err == nil {
			t := themeOf(r)
			t.Created, t.Updated = "", ""
			out.Theme = &t
		}
	}
	c := s.d.Client(ctx)
	st := s.settings(ctx)
	if f.Sinks != nil && f.Sinks.Events && !c.OptedOut && s.allowed(ctx, "form:view:"+c.IP, st) {
		s.emit(ctx, org, Event{Name: Viewed, Form: f.ID, Public: f.PublicID})
	}
	return out, nil
}

// allowed spends one of the address's submissions; a caller with no address is
// inside the cluster.
func (s *service) allowed(ctx context.Context, key string, st Settings) bool {
	if strings.HasSuffix(key, ":") || s.d.Allow == nil {
		return true
	}
	return s.d.Allow(ctx, key, st.Limits.Rate, st.window())
}

// Answers a published form. No credential is needed.
//
// The answers are read against the form's own fields: each names a field (by
// ref) once and carries the one value its type takes, inside its bounds; a
// completed submission answers every required field. Hidden values are the
// names the form declares. The response is from the signed-in person when there
// is one (their account, from their token, never from the body), else from the
// email field's answer, else from the page's visitor id.
//
// Idempotent by token: the same token is the same response, so a submission
// sent twice files one. With completed false the answers so far are saved and
// the response stays open; a completed response is final and a later submission
// under its token is answered with it unchanged. A subscribe form's response is
// keyed by its address instead: one address is one subscriber.
//
// A completed response goes to the form's sinks: the event stream, the CRM, the
// forwarding address, the welcome mail and the webhooks.
//
// 429 past the platform's rate for one address, 413 past its body bound, 400
// for a body that does not decode, a field no person fills, or any problem with
// the answers (every one named), 404 for a form that is not published.
//
// Example: {"token":"r_9f1c2a7b","answers":[{"field":"rating","number":5},{"field":"comment","text":"Loved it"}],"hidden":{"video":"7mVTEAHHjPc"},"landed_at":"2026-11-02T08:34:10Z","visitor":"v_k3j2h1","path":"/beta"}
func (s *service) submit(ctx context.Context, in *Submission) (*Receipt, error) {
	st := s.settings(ctx)
	c := s.d.Client(ctx)
	if !s.allowed(ctx, "form:response:"+c.IP, st) {
		return nil, zip.Errorf(http.StatusTooManyRequests, "form: too many submissions; try again later")
	}
	if in.size > st.Limits.Body {
		return nil, zip.Errorf(http.StatusRequestEntityTooLarge, "form: the submission is larger than %d bytes", st.Limits.Body)
	}
	if in.malformed {
		return nil, zip.ErrBadRequest("form: the submission does not decode")
	}
	if strings.TrimSpace(in.Website) != "" {
		return nil, zip.ErrBadRequest("form: the submission was refused")
	}
	org, f, err := s.resolve(ctx, in.ID)
	if err != nil {
		return nil, err
	}

	answers, problems := checkAnswers(*f, in.Answers, st)
	completed := in.Completed == nil || *in.Completed
	if completed {
		if gaps := missing(*f, answers); len(gaps) > 0 {
			problems = append(problems, "unanswered: "+strings.Join(gaps, ", "))
		}
	}
	hidden := map[string]string{}
	for name, v := range in.Hidden {
		if !containsString(f.Hidden, name) {
			problems = append(problems, "hidden: the form takes no value "+quote(name))
			continue
		}
		v, ok := line(v, 500)
		if !ok {
			problems = append(problems, "hidden: "+name+" is one line of at most 500 characters")
			continue
		}
		hidden[name] = v
	}
	token := strings.TrimSpace(in.Token)
	if token != "" && !tokenShape.MatchString(token) {
		problems = append(problems, "token is 8 to 64 letters, digits, dashes and underscores")
	}
	visitor := strings.TrimSpace(in.Visitor)
	if visitor != "" && !visitorShape.MatchString(visitor) {
		problems = append(problems, "visitor is letters, digits, dot, dash and underscore, at most 64")
	}
	path, ok := line(in.Path, 500)
	if !ok {
		problems = append(problems, "path is one line of at most 500 characters")
	}
	if len(problems) > 0 {
		return nil, zip.ErrBadRequest("form: " + strings.Join(problems, "; "))
	}
	if token == "" {
		token = "t_" + fresh()
	}

	who := s.respondent(ctx, *f, answers, visitor)
	id := derived("response", f.ID+"\x00"+token)
	if email := strings.ToLower(textOf(*f, answers, subscriber(*f))); f.Kind == Subscribe && email != "" {
		id = derived("response", f.ID+"\x00"+email)
	}
	now := s.d.Now().UTC()
	held, err := s.d.Store.Get(ctx, org, Responses, id)
	switch {
	case errors.Is(err, ErrNotFound):
		held = nil
	case err != nil:
		return nil, s.failed("the response could not be read", org, err)
	}
	if held != nil {
		if r := responseOf(held); r.Completed {
			return &Receipt{ID: r.ID, Completed: true, SubmittedAt: r.SubmittedAt, Thanks: f.Thanks}, nil
		}
	}
	rec := Record{
		"id": id, "form": f.ID, "token": token, "completed": completed, "answers": answers, "hidden": hidden,
		"respondent": who, "email": who.Email, "person": who.PersonID, "last": last(answers), "path": path, "source": "web",
	}
	if held == nil {
		rec["at"] = baseTime(now)
	}
	// A landing time is the page's word: one in the future, or more than 30 days
	// back, is no landing this response had, and it is kept as none.
	landed := when(in.LandedAt)
	if landed.After(now) || now.Sub(landed) >= 30*24*time.Hour {
		landed = time.Time{}
	}
	if !landed.IsZero() {
		rec["landed"] = baseTime(landed)
	}
	if completed {
		rec["submitted"] = baseTime(now)
		if !landed.IsZero() {
			rec["duration"] = int(now.Sub(landed).Seconds())
		}
	}
	if err := s.d.Store.Put(ctx, org, Responses, rec); err != nil {
		return nil, s.failed("the response could not be saved", org, err)
	}
	out := &Receipt{ID: id, Completed: completed}
	if f.Sinks != nil && f.Sinks.Events && !c.OptedOut {
		ev := Event{Form: f.ID, Public: f.PublicID, Response: id, PersonID: who.PersonID, Visitor: who.Visitor, Fields: refs(answers)}
		if held == nil {
			ev.Name = Started
			s.emit(ctx, org, ev)
		}
		if completed {
			ev.Name = Submitted
			s.emit(ctx, org, ev)
		}
	}
	if completed {
		out.SubmittedAt, out.Thanks = stamp(now), f.Thanks
		r := Response{
			ID: id, Form: f.ID, Token: token, Completed: true, LandedAt: stamp(landed), SubmittedAt: stamp(now),
			Answers: answers, Hidden: hidden, Respondent: who, Path: path, Source: "web",
		}
		s.route(org, *f, r)
	}
	s.expire(org, f.ID, st)
	return out, nil
}

// respondent is who a response is from: the signed-in person, from their token;
// else the address the CRM mapping's email field (or the first email field)
// answers; else the visitor.
func (s *service) respondent(ctx context.Context, f Form, answers []Answer, visitor string) Respondent {
	c := s.d.Caller(ctx)
	if c.Subject != "" && address(c.Email) {
		email := strings.ToLower(c.Email)
		return Respondent{Kind: "account", Subject: c.Subject, Email: email, PersonID: s.d.Person(email), Visitor: visitor}
	}
	if email := strings.ToLower(textOf(f, answers, emailRef(f))); address(email) {
		return Respondent{Kind: "email", Email: email, PersonID: s.d.Person(email), Visitor: visitor}
	}
	if visitor != "" {
		return Respondent{Kind: "visitor", Visitor: visitor}
	}
	return Respondent{Kind: "none"}
}

// emailRef is the field whose answer is the respondent's address: the CRM
// mapping's, else the form's first email field.
func emailRef(f Form) string {
	if f.Sinks != nil && f.Sinks.CRM != nil && f.Sinks.CRM.Email != "" {
		return f.Sinks.CRM.Email
	}
	for _, fd := range f.Fields {
		if fd.Type == Email {
			return fd.Ref
		}
	}
	return ""
}

// subscriber is a subscribe form's required email field: the address that is
// the subscriber.
func subscriber(f Form) string {
	for _, fd := range f.Fields {
		if fd.Type == Email && fd.Required {
			return fd.Ref
		}
	}
	return ""
}

// last is the ref of the last field answered, in form order.
func last(answers []Answer) string {
	if len(answers) == 0 {
		return ""
	}
	return answers[len(answers)-1].Field
}

// refs are the refs of the fields answered.
func refs(answers []Answer) []string {
	out := make([]string, 0, len(answers))
	for _, a := range answers {
		out = append(out, a.Field)
	}
	return out
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func quote(s string) string { return `"` + clip(s, 64) + `"` }
