package form

// check.go — reading a form a caller wrote: every field normalized, every id
// assigned, every bound applied, and every problem named at once.

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	slugShape   = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)
	refShape    = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)
	hiddenShape = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)
	langShape   = regexp.MustCompile(`^[a-z]{2,3}(?:-[A-Za-z0-9]{2,8})?$`)
	colorShape  = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	fontShape   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 -]{0,63}$`)
)

// Bounds on the text a form carries.
const (
	maxTitle       = 200
	maxQuestion    = 500
	maxDescription = 1000
	maxScreenText  = 2000
	maxButton      = 64
	maxLabel       = 200
	maxTags        = 20
	maxTag         = 64
	maxSubject     = 200
	maxWelcome     = 10000
)

// line is s trimmed, when it is one line of printable text of at most n
// characters; ok is false otherwise.
func line(s string, n int) (string, bool) {
	s = strings.TrimSpace(s)
	return s, utf8.ValidString(s) && utf8.RuneCountInString(s) <= n && !strings.ContainsFunc(s, unicode.IsControl)
}

// prose is s trimmed, when it is text of at most n characters with no control
// character but line breaks and tabs.
func prose(s string, n int) (string, bool) {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	return s, utf8.ValidString(s) && utf8.RuneCountInString(s) <= n &&
		!strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' })
}

// https is s when it is one https address with a host and no credentials.
func https(s string) (string, bool) {
	s = strings.TrimSpace(s)
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || len(s) > 2000 || strings.ContainsAny(s, " \t\r\n\"<>\\") {
		return "", false
	}
	return s, true
}

// address reports whether s is shaped like one email address: one @, something
// before it and a dotted domain after it, and none of the characters that join or
// quote a second address.
func address(s string) bool {
	local, domain, ok := strings.Cut(s, "@")
	return ok && local != "" && len(s) <= 320 && !strings.ContainsAny(s, " \t\r\n,;%\"<>()[]\\") &&
		!strings.Contains(domain, "@") && strings.Contains(strings.Trim(domain, "."), ".")
}

// checkForm normalizes f, assigns its ids and answers every problem with it.
// held is the form as stored, nil for a new one: a type the platform turned off
// is refused only where it is new.
func checkForm(f Form, st Settings, held *Form) (Form, []string) {
	var p []string
	bad := func(format string, a ...any) { p = append(p, fmt.Sprintf(format, a...)) }

	f.Slug = strings.TrimSpace(f.Slug)
	if f.Slug != "" && !slugShape.MatchString(f.Slug) {
		bad("slug is lowercase letters, digits and dashes, at most 64")
	}
	if held != nil && held.Slug != "" && f.Slug != held.Slug {
		bad("a form's slug is its id and does not change")
	}
	if f.Kind = strings.TrimSpace(f.Kind); f.Kind == "" {
		f.Kind = Plain
	}
	if !slices.Contains(Kinds, f.Kind) {
		bad("kind is one of %s", strings.Join(Kinds, ", "))
	}
	var ok bool
	if f.Title, ok = line(f.Title, maxTitle); !ok || f.Title == "" {
		bad("title is one line of at most %d characters, and required", maxTitle)
	}

	asked := map[string]bool{}
	if held != nil {
		for _, h := range held.Fields {
			asked[h.Type] = true
		}
	}
	if len(f.Fields) == 0 {
		bad("a form asks at least one field")
	}
	if len(f.Fields) > st.Limits.Fields {
		bad("a form asks at most %d fields", st.Limits.Fields)
	}
	refs, ids := map[string]bool{}, map[string]bool{}
	for i := range f.Fields {
		fp := checkField(&f.Fields[i], st, asked)
		for _, x := range fp {
			bad("fields[%d]: %s", i, x)
		}
		fd := f.Fields[i]
		if refs[fd.Ref] {
			bad("fields[%d]: ref %q is used twice", i, fd.Ref)
		}
		if ids[fd.ID] {
			bad("fields[%d]: id %q is used twice", i, fd.ID)
		}
		refs[fd.Ref], ids[fd.ID] = true, true
	}
	if f.Kind == Subscribe && !slices.ContainsFunc(f.Fields, func(x Field) bool { return x.Type == Email && x.Required }) {
		bad("a subscribe form asks a required email field")
	}

	f.Welcome = checkScreen(f.Welcome, false, func(s string) { bad("welcome: %s", s) })
	f.Thanks = checkScreen(f.Thanks, true, func(s string) { bad("thanks: %s", s) })

	if len(f.Hidden) > st.Limits.Hidden {
		bad("a form names at most %d hidden values", st.Limits.Hidden)
	}
	seen := map[string]bool{}
	for _, h := range f.Hidden {
		if !hiddenShape.MatchString(h) || seen[h] {
			bad("hidden %q is lowercase letters, digits and underscores, at most 32, named once", h)
		}
		seen[h] = true
	}

	o := &f.Settings
	if o.Visibility = strings.TrimSpace(o.Visibility); o.Visibility == "" {
		o.Visibility = "public"
	}
	if o.Visibility != "public" && o.Visibility != "private" {
		bad("settings.visibility is public or private")
	}
	if o.Language = strings.TrimSpace(o.Language); o.Language == "" {
		o.Language = "en"
	}
	if !langShape.MatchString(o.Language) {
		bad("settings.language is a tag like en or pt-BR")
	}
	if o.Mode = strings.TrimSpace(o.Mode); o.Mode == "" {
		o.Mode = "one"
	}
	if o.Mode != "one" && o.Mode != "all" {
		bad("settings.mode is one or all")
	}

	if f.Theme = strings.TrimSpace(f.Theme); f.Theme != "" && !recordID(f.Theme) {
		bad("theme is a theme's id")
	}
	if len(f.Tags) > maxTags {
		bad("a form has at most %d tags", maxTags)
	}
	for i, t := range f.Tags {
		if f.Tags[i], ok = line(t, maxTag); !ok || f.Tags[i] == "" {
			bad("tags[%d] is one line of at most %d characters", i, maxTag)
		}
	}

	sinks := st.Sinks
	if f.Sinks != nil {
		sinks = *f.Sinks
	}
	sinks, sp := checkSinks(sinks, &f)
	p = append(p, sp...)
	f.Sinks = &sinks
	return f, p
}

// checkField normalizes one field and answers its problems. asked are the types
// the stored form already asks.
func checkField(fd *Field, st Settings, asked map[string]bool) []string {
	var p []string
	bad := func(format string, a ...any) { p = append(p, fmt.Sprintf(format, a...)) }
	fd.Type = strings.TrimSpace(fd.Type)
	rule := st.rule(fd.Type)
	switch {
	case !slices.Contains(Types, fd.Type):
		bad("type %q is not a field type", fd.Type)
		return p
	case !rule.Enabled && !asked[fd.Type]:
		bad("type %s is not offered", fd.Type)
	}
	fd.Ref, fd.ID = strings.TrimSpace(fd.Ref), strings.TrimSpace(fd.ID)
	switch {
	case fd.ID == "" && fd.Ref == "":
		fd.ID = fresh()[:8]
		fd.Ref = fd.ID
	case fd.ID == "":
		fd.ID = derived("field", fd.Ref)[:8]
	case fd.Ref == "":
		fd.Ref = fd.ID
	}
	if !refShape.MatchString(fd.Ref) || !refShape.MatchString(fd.ID) {
		bad("id and ref are lowercase letters, digits, dash and underscore, at most 64")
	}
	var ok bool
	if fd.Title, ok = prose(fd.Title, maxQuestion); !ok || fd.Title == "" {
		bad("title is at most %d characters, and required", maxQuestion)
	}
	if fd.Description, ok = prose(fd.Description, maxDescription); !ok {
		bad("description is at most %d characters", maxDescription)
	}
	if fd.Type == Statement {
		fd.Required = false
	}

	choice := fd.Type == MultiChoice || fd.Type == Dropdown
	if !choice && len(fd.Choices) > 0 {
		bad("only multiple_choice and dropdown offer choices")
	}
	if fd.Multiple && fd.Type != MultiChoice {
		bad("only multiple_choice takes several choices")
	}
	if choice {
		if len(fd.Choices) == 0 {
			bad("a %s offers at least one choice", fd.Type)
		}
		if rule.Max > 0 && len(fd.Choices) > rule.Max {
			bad("a %s offers at most %d choices", fd.Type, rule.Max)
		}
		cref := map[string]bool{}
		for i := range fd.Choices {
			c := &fd.Choices[i]
			c.ID, c.Ref = strings.TrimSpace(c.ID), strings.TrimSpace(c.Ref)
			switch {
			case c.ID == "" && c.Ref == "":
				c.ID = fresh()[:8]
				c.Ref = c.ID
			case c.ID == "":
				c.ID = derived("choice", fd.Ref+"\x00"+c.Ref)[:8]
			case c.Ref == "":
				c.Ref = c.ID
			}
			if !refShape.MatchString(c.Ref) || !refShape.MatchString(c.ID) {
				bad("choices[%d]: id and ref are lowercase letters, digits, dash and underscore", i)
			}
			if cref[c.Ref] {
				bad("choices[%d]: ref %q is used twice", i, c.Ref)
			}
			cref[c.Ref] = true
			if c.Label, ok = line(c.Label, maxLabel); !ok || c.Label == "" {
				bad("choices[%d]: label is one line of at most %d characters, and required", i, maxLabel)
			}
		}
	}

	switch fd.Type {
	case Rating:
		if fd.Steps == 0 {
			fd.Steps = 5
		}
		if fd.Steps < 3 || fd.Steps > min(10, max(rule.Max, 3)) {
			bad("a rating has 3 to %d steps", min(10, max(rule.Max, 3)))
		}
	case OpinionScale:
		if fd.Steps == 0 {
			fd.Steps = 11
		}
		if fd.Steps < 5 || fd.Steps > min(11, max(rule.Max, 5)) {
			bad("an opinion_scale has 5 to %d steps", min(11, max(rule.Max, 5)))
		}
		if fd.Start != 0 && fd.Start != 1 {
			bad("an opinion_scale starts at 0 or 1")
		}
	default:
		if fd.Steps != 0 || fd.Start != 0 {
			bad("only rating and opinion_scale have steps")
		}
	}
	if fd.Labels != nil {
		if fd.Type != OpinionScale {
			bad("only an opinion_scale has labels")
		}
		for _, l := range []*string{&fd.Labels.Left, &fd.Labels.Center, &fd.Labels.Right} {
			if *l, ok = line(*l, 64); !ok {
				bad("a label is one line of at most 64 characters")
			}
		}
	}
	if v := fd.Validations; v != nil {
		text := fd.Type == ShortText || fd.Type == LongText
		if v.MaxLength != 0 && (!text || v.MaxLength < 1 || v.MaxLength > rule.Max) {
			bad("validations.max_length is 1 to %d on a short_text or long_text", rule.Max)
		}
		if (v.MinValue != nil || v.MaxValue != nil) && fd.Type != Number {
			bad("validations.min_value and max_value bound a number")
		}
		if v.MinValue != nil && v.MaxValue != nil && *v.MinValue > *v.MaxValue {
			bad("validations.min_value is above max_value")
		}
	}
	return p
}

// checkScreen normalizes a screen; nil stays nil.
func checkScreen(s *Screen, thanks bool, bad func(string)) *Screen {
	if s == nil {
		return nil
	}
	var ok bool
	if s.Title, ok = line(s.Title, maxTitle); !ok {
		bad(fmt.Sprintf("title is one line of at most %d characters", maxTitle))
	}
	if s.Description, ok = prose(s.Description, maxScreenText); !ok {
		bad(fmt.Sprintf("description is at most %d characters", maxScreenText))
	}
	if s.Button, ok = line(s.Button, maxButton); !ok {
		bad(fmt.Sprintf("button is one line of at most %d characters", maxButton))
	}
	if s.Redirect != "" {
		if !thanks {
			bad("only the thank-you screen redirects")
		} else if s.Redirect, ok = https(s.Redirect); !ok {
			bad("redirect is an https address")
		}
	}
	return s
}

// checkSinks normalizes sinks and answers their problems. f is the form they
// route, nil for the platform's defaults, where no field can be named.
func checkSinks(s Sinks, f *Form) (Sinks, []string) {
	var p []string
	typeOf := func(ref string) string {
		if f == nil {
			return ""
		}
		for _, fd := range f.Fields {
			if fd.Ref == ref {
				return fd.Type
			}
		}
		return ""
	}
	if c := s.CRM; c != nil {
		for _, m := range []struct {
			name string
			ref  *string
			want []string
		}{
			{"email", &c.Email, []string{Email}},
			{"name", &c.Name, []string{ShortText}},
			{"company", &c.Company, []string{ShortText}},
		} {
			*m.ref = strings.TrimSpace(*m.ref)
			if *m.ref == "" {
				continue
			}
			if f == nil {
				p = append(p, fmt.Sprintf("sinks.crm.%s names a field, and the platform's defaults name none", m.name))
				continue
			}
			if t := typeOf(*m.ref); !slices.Contains(m.want, t) {
				p = append(p, fmt.Sprintf("sinks.crm.%s names a %s field of this form", m.name, strings.Join(m.want, " or ")))
			}
		}
		var ok bool
		if c.List, ok = line(c.List, 100); !ok {
			p = append(p, "sinks.crm.list is one line of at most 100 characters")
		}
	}
	if fw := s.Forward; fw != nil {
		fw.Email = strings.TrimSpace(fw.Email)
		if !address(fw.Email) {
			p = append(p, "sinks.forward.email is an email address")
		}
		var ok bool
		if fw.Name, ok = line(fw.Name, 200); !ok {
			p = append(p, "sinks.forward.name is one line of at most 200 characters")
		}
	}
	if w := s.Welcome; w != nil {
		var ok bool
		if w.Subject, ok = line(w.Subject, maxSubject); !ok || w.Subject == "" {
			p = append(p, fmt.Sprintf("sinks.welcome.subject is one line of at most %d characters, and required", maxSubject))
		}
		if w.Body, ok = prose(w.Body, maxWelcome); !ok || w.Body == "" {
			p = append(p, fmt.Sprintf("sinks.welcome.body is at most %d characters, and required", maxWelcome))
		}
		if f != nil && !slices.ContainsFunc(f.Fields, func(x Field) bool { return x.Type == Email }) {
			p = append(p, "sinks.welcome mails the respondent, so the form asks an email field")
		}
	}
	return s, p
}

// checkTheme normalizes a theme and answers its problems.
func checkTheme(t Theme) (Theme, []string) {
	var p []string
	var ok bool
	if t.Name, ok = line(t.Name, 100); !ok || t.Name == "" {
		p = append(p, "name is one line of at most 100 characters, and required")
	}
	for name, c := range map[string]*string{"question": &t.Colors.Question, "answer": &t.Colors.Answer, "button": &t.Colors.Button, "background": &t.Colors.Background} {
		if *c = strings.TrimSpace(*c); *c != "" && !colorShape.MatchString(*c) {
			p = append(p, fmt.Sprintf("colors.%s is #rrggbb", name))
		}
	}
	if t.Font = strings.TrimSpace(t.Font); t.Font != "" && !fontShape.MatchString(t.Font) {
		p = append(p, "font is letters, digits, spaces and dashes, at most 64")
	}
	if t.Background != "" {
		if t.Background, ok = https(t.Background); !ok {
			p = append(p, "background is an https address")
		}
	}
	return t, p
}
