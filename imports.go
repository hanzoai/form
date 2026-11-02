package form

// imports.go — responses brought in from elsewhere, keeping when they were made.

import (
	"context"
	"strings"

	"github.com/zap-proto/zip"
)

// ImportIn is a batch of responses made elsewhere, for one form.
type ImportIn struct {
	// ID is the form's id or slug, from the path.
	ID string `json:"id"`
	// Responses are the responses, at most 500.
	Responses []Imported `json:"responses"`
	// File also files each new response in the CRM, at the time it was made.
	// Nothing is mailed and no webhook is told.
	File bool `json:"file"`
}

// Imported is one response made elsewhere.
type Imported struct {
	// External is its id where it was made: at most 200 characters, and the
	// response's key, so importing it again is the same response.
	External string `json:"external"`
	// At is when it was made, RFC 3339. Required.
	At string `json:"at"`
	// Answers are its answers, each read against the form's field as a
	// submission's is; a required field may be unanswered.
	Answers []Answer `json:"answers"`
	// Hidden are values it carried, by the names the form declares.
	Hidden map[string]string `json:"hidden"`
	// Email is the address it was from, when the source knew one. On a subscribe
	// form the address is the response's key: one address is one subscriber,
	// however many times and places it subscribed.
	Email string `json:"email"`
	// Source is where it was made, at most 100 characters: commerce, a list's name.
	Source string `json:"source"`
}

// Imports is what a batch did.
type Imports struct {
	// Created counts the responses made new.
	Created int `json:"created"`
	// Existing counts the ones already held: imported before, or the same
	// subscriber.
	Existing int `json:"existing"`
	// Failed are the ones refused, with why.
	Failed []ImportFailure `json:"failed"`
}

// ImportFailure is one imported response refused.
type ImportFailure struct {
	External string `json:"external"`
	Reason   string `json:"reason"`
}

// maxImport bounds one batch: what the store writes in one transaction.
const maxImport = 500

// Brings responses made elsewhere into a form, keeping when each was made, a
// batch of at most 500 at a time. Each is keyed by its external id (on a
// subscribe form, by its address), so a batch sent again, or an import resumed
// from any point, makes nothing twice. Each answer is read against its field as
// a submission's is; a required field may be unanswered. With file, each new
// response is also filed in the CRM at the time it was made; nothing is mailed
// and no webhook is told. An org admin only.
//
// Example: {"responses":[{"external":"sub_4XkQ","at":"2017-03-04T12:00:00Z","email":"ada@example.com","source":"commerce"}],"file":true}
func (s *service) importResponses(ctx context.Context, in *ImportIn) (*Imports, error) {
	org, err := s.change(ctx)
	if err != nil {
		return nil, err
	}
	f, err := s.lookup(ctx, org, in.ID)
	if err != nil {
		return nil, err
	}
	if len(in.Responses) > maxImport {
		return nil, zip.ErrBadRequest("form: at most 500 responses a batch")
	}
	st := s.settings(ctx)
	out := &Imports{Failed: []ImportFailure{}}
	type pending struct {
		rec Record
		r   Response
	}
	byID := map[string]pending{}
	var order []string
	for _, im := range in.Responses {
		ext, ok := line(im.External, 200)
		fail := func(reason string) {
			out.Failed = append(out.Failed, ImportFailure{External: clip(ext, 200), Reason: reason})
		}
		if !ok || ext == "" {
			fail("external is one line of at most 200 characters, and required")
			continue
		}
		at := when(im.At)
		if at.IsZero() {
			fail("at is an RFC 3339 time")
			continue
		}
		answers, problems := checkAnswers(*f, im.Answers, st)
		if len(problems) > 0 {
			fail(strings.Join(problems, "; "))
			continue
		}
		hidden := map[string]string{}
		for name, v := range im.Hidden {
			if v, ok := line(v, 500); ok && containsString(f.Hidden, name) {
				hidden[name] = v
			}
		}
		email := strings.ToLower(strings.TrimSpace(im.Email))
		if email == "" {
			email = strings.ToLower(textOf(*f, answers, emailRef(*f)))
		}
		if email != "" && !address(email) {
			fail("email is not an email address")
			continue
		}
		source, ok := line(im.Source, 100)
		if !ok {
			fail("source is one line of at most 100 characters")
			continue
		}
		id := derived("response", f.ID+"\x00import\x00"+ext)
		if f.Kind == Subscribe {
			if email == "" {
				fail("a subscribe form's response is an address")
				continue
			}
			id = derived("response", f.ID+"\x00"+email)
		}
		if _, dup := byID[id]; dup {
			out.Existing++
			continue
		}
		who := Respondent{Kind: "none"}
		if email != "" {
			who = Respondent{Kind: "email", Email: email, PersonID: s.d.Person(email)}
		}
		if source == "" {
			source = "import"
		}
		r := Response{ID: id, Form: f.ID, Completed: true, SubmittedAt: stamp(at), Answers: answers, Hidden: hidden, Respondent: who, Source: source, External: ext}
		byID[id] = pending{r: r, rec: Record{
			"id": id, "form": f.ID, "token": "", "completed": true, "answers": answers, "hidden": hidden, "respondent": who,
			"email": email, "person": who.PersonID, "last": last(answers), "source": source, "external": ext,
			"at": baseTime(at), "submitted": baseTime(at),
		}}
		order = append(order, id)
	}
	held, err := s.held(ctx, org, order)
	if err != nil {
		return nil, err
	}
	var recs []Record
	for _, id := range order {
		if held[id] {
			out.Existing++
			continue
		}
		recs = append(recs, byID[id].rec)
	}
	if len(recs) > 0 {
		if err := s.d.Store.Put(ctx, org, Responses, recs...); err != nil {
			return nil, s.failed("the batch could not be saved", org, err)
		}
	}
	out.Created = len(recs)
	// Every response of the batch is filed, new or held: a filing is keyed by its
	// response, so a batch sent again after a failure files what it missed and
	// nothing twice.
	if in.File && f.Sinks != nil && f.Sinks.CRM != nil && s.d.File != nil {
		for _, id := range order {
			if err := s.d.File(ctx, org, s.filing(*f, byID[id].r)); err != nil {
				return nil, s.failed("an imported response could not be filed in the CRM; the batch is saved, send it again to file the rest", org, err)
			}
		}
	}
	return out, nil
}

// held reports which of ids the org's responses already hold, asking 90 at a time.
func (s *service) held(ctx context.Context, org string, ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	for start := 0; start < len(ids); start += 90 {
		chunk := ids[start:min(start+90, len(ids))]
		var c []Record
		for _, id := range chunk {
			c = append(c, leaf("id", "is", id))
		}
		got, err := s.d.Store.List(ctx, org, Responses, Query{Where: or(c...), PerPage: len(chunk)})
		if err != nil {
			return nil, s.failed("the batch could not be checked", org, err)
		}
		for _, r := range got.Items {
			out[field(r, "id")] = true
		}
	}
	return out, nil
}
