package form

// settings.go — the platform's configuration of forms: which field types are
// offered and how large each may be, the limits every form and submission is
// held to, the sinks a new form takes when it names none, and how long a
// response is kept. The host stores it and serves the typed admin ops that edit
// it; this file is its shape, its defaults and its check.

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Settings is the platform's configuration of forms.
type Settings struct {
	// Types are the field types, each with whether forms may use it and its bound.
	Types []TypeRule `json:"types"`
	// Limits bound every form and every submission.
	Limits Limits `json:"limits"`
	// Sinks are what a new form routes its responses to when it names nothing.
	Sinks Sinks `json:"sinks"`
	// Retention is how many days a response is kept; 0 keeps it. A response past
	// it is deleted the next time its form is answered or its responses are read.
	Retention int `json:"retention"`
}

// TypeRule is one field type as the platform offers it.
type TypeRule struct {
	// Type is the field type.
	Type string `json:"type"`
	// Enabled is whether a form may ask it. A form already asking a type that is
	// turned off keeps answering it; a write that adds one is refused.
	Enabled bool `json:"enabled"`
	// Max is the type's bound: characters for short_text, long_text, email,
	// phone and url; points for rating and opinion_scale; choices for
	// multiple_choice and dropdown. 0 for the types that have none.
	Max int `json:"max"`
}

// Limits bound every form and every submission.
type Limits struct {
	// Fields is the most fields one form has.
	Fields int `json:"fields"`
	// Hidden is the most hidden values one form names.
	Hidden int `json:"hidden"`
	// Body is the most bytes one submission's body holds.
	Body int `json:"body"`
	// Rate is how many submissions one address makes in Window seconds.
	Rate int `json:"rate"`
	// Window is the rate's window, in seconds.
	Window int `json:"window"`
	// Webhooks is the most webhooks one form has.
	Webhooks int `json:"webhooks"`
}

// Defaults is the configuration the service runs with when none is saved.
func Defaults() Settings {
	maxOf := map[string]int{
		ShortText: 255, LongText: 10000, Email: 320, Phone: 32, URL: 2000,
		Rating: 10, OpinionScale: 11, MultiChoice: 50, Dropdown: 500,
	}
	var types []TypeRule
	for _, t := range Types {
		types = append(types, TypeRule{Type: t, Enabled: true, Max: maxOf[t]})
	}
	return Settings{
		Types:  types,
		Limits: Limits{Fields: 100, Hidden: 10, Body: 64 << 10, Rate: 30, Window: 3600, Webhooks: 10},
		Sinks:  Sinks{Events: true, CRM: &CRM{}},
	}
}

// ceilings are the bounds no saved configuration may pass: what the store and the
// wire hold, not a policy.
var ceilings = map[string]int{
	ShortText: 1000, LongText: 100000, Email: 320, Phone: 32, URL: 2000,
	Rating: 10, OpinionScale: 11, MultiChoice: 200, Dropdown: 1000,
}

// Check reads a configuration a SuperAdmin wrote: every known type exactly once,
// each bound inside its ceiling, every limit positive and inside its own. It
// answers the configuration as it will be stored and every problem at once.
func (st Settings) Check() (Settings, []string) {
	var problems []string
	seen := map[string]bool{}
	var types []TypeRule
	for _, r := range st.Types {
		r.Type = strings.TrimSpace(r.Type)
		switch {
		case !slices.Contains(Types, r.Type):
			problems = append(problems, fmt.Sprintf("%q is not a field type", r.Type))
			continue
		case seen[r.Type]:
			problems = append(problems, fmt.Sprintf("%s is named twice", r.Type))
			continue
		}
		seen[r.Type] = true
		ceiling, bounded := ceilings[r.Type]
		switch {
		case !bounded && r.Max != 0:
			problems = append(problems, fmt.Sprintf("%s has no bound to set", r.Type))
		case bounded && (r.Max < 1 || r.Max > ceiling):
			problems = append(problems, fmt.Sprintf("%s's bound is 1 to %d", r.Type, ceiling))
		}
		if r.Type == Rating && r.Max < 3 || r.Type == OpinionScale && r.Max < 5 {
			problems = append(problems, fmt.Sprintf("%s's bound is too small for any scale", r.Type))
		}
		types = append(types, r)
	}
	for _, t := range Types {
		if !seen[t] {
			problems = append(problems, fmt.Sprintf("%s is missing: every field type is listed, enabled or not", t))
		}
	}
	l := st.Limits
	for _, b := range []struct {
		name      string
		v, lo, hi int
	}{
		{"fields", l.Fields, 1, 500}, {"hidden", l.Hidden, 0, 50}, {"body", l.Body, 1 << 10, 1 << 20},
		{"rate", l.Rate, 1, 10000}, {"window", l.Window, 1, 86400}, {"webhooks", l.Webhooks, 0, 50},
	} {
		if b.v < b.lo || b.v > b.hi {
			problems = append(problems, fmt.Sprintf("limits.%s is %d to %d", b.name, b.lo, b.hi))
		}
	}
	if st.Retention < 0 || st.Retention > 36500 {
		problems = append(problems, "retention is 0 (keep) to 36500 days")
	}
	sinks, sinkProblems := checkSinks(st.Sinks, nil)
	problems = append(problems, sinkProblems...)
	return Settings{Types: types, Limits: l, Sinks: sinks, Retention: st.Retention}, problems
}

// rule is the platform's rule for one type; a type it does not list is off.
func (st Settings) rule(t string) TypeRule {
	for _, r := range st.Types {
		if r.Type == t {
			return r
		}
	}
	return TypeRule{Type: t}
}

// window is the submission rate's window.
func (st Settings) window() time.Duration { return time.Duration(st.Limits.Window) * time.Second }

// settings is the configuration in force: the host's, else the defaults, and a
// stored one missing a part falls back to the default for that part.
func (s *service) settings(ctx context.Context) Settings {
	st := s.d.Settings(ctx)
	if len(st.Types) == 0 {
		st.Types = Defaults().Types
	}
	if st.Limits == (Limits{}) {
		st.Limits = Defaults().Limits
	}
	return st
}
