package form

// answer.go — reading what a respondent said, field by field, against the
// field's own type and bounds; and saying it back as text.

import (
	"fmt"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// phoneShape is a phone number: 3 to 20 digits, an optional leading +, and
// spaces, dots, dashes or brackets between them.
var phoneShape = regexp.MustCompile(`^\+?[0-9 ().-]{3,32}$`)

// checkAnswers reads a submission's answers against the form: each names a field
// once, carries the one value its type takes, inside the field's bounds. It
// answers them in the form's field order, and every problem at once.
func checkAnswers(f Form, in []Answer, st Settings) ([]Answer, []string) {
	var p []string
	byRef := map[string]Answer{}
	for i, a := range in {
		a.Field = strings.TrimSpace(a.Field)
		fd, ok := fieldOf(f, a.Field)
		switch {
		case !ok:
			p = append(p, fmt.Sprintf("answers[%d]: the form has no field %q", i, a.Field))
			continue
		case byRef[fd.Ref].Field != "":
			p = append(p, fmt.Sprintf("answers[%d]: %s is answered twice", i, fd.Ref))
			continue
		}
		got, problem := checkAnswer(fd, a, st.rule(fd.Type))
		if problem != "" {
			p = append(p, fmt.Sprintf("%s: %s", fd.Ref, problem))
			continue
		}
		if got != nil {
			byRef[fd.Ref] = *got
		}
	}
	out := []Answer{}
	for _, fd := range f.Fields {
		if a, ok := byRef[fd.Ref]; ok {
			out = append(out, a)
		}
	}
	return out, p
}

// missing are the required fields answers leave unanswered.
func missing(f Form, answers []Answer) []string {
	var out []string
	for _, fd := range f.Fields {
		if fd.Required && !slices.ContainsFunc(answers, func(a Answer) bool { return a.Field == fd.Ref }) {
			out = append(out, fd.Ref)
		}
	}
	return out
}

// fieldOf is the field a ref (or an id) names.
func fieldOf(f Form, ref string) (Field, bool) {
	for _, fd := range f.Fields {
		if fd.Ref == ref || fd.ID == ref {
			return fd, true
		}
	}
	return Field{}, false
}

// checkAnswer reads one answer. A nil answer with no problem is a field left
// blank: text that is empty, or no choice picked.
func checkAnswer(fd Field, a Answer, rule TypeRule) (*Answer, string) {
	out := Answer{Field: fd.Ref, Type: fd.Type}
	if a.Text != nil && strings.TrimSpace(*a.Text) == "" {
		a.Text = nil
	}
	set := 0
	for _, v := range []bool{a.Text != nil, a.Number != nil, a.Boolean != nil, len(a.Choices) > 0, strings.TrimSpace(a.Date) != ""} {
		if v {
			set++
		}
	}
	if fd.Type == Statement {
		return nil, "a statement is not answered"
	}
	if set == 0 {
		return nil, ""
	}
	only := func(ok bool) string {
		if !ok || set != 1 {
			return "carries exactly the one value its type takes"
		}
		return ""
	}
	switch fd.Type {
	case ShortText, LongText, Email, Phone, URL:
		if s := only(a.Text != nil); s != "" {
			return nil, s + ": text"
		}
		bound := rule.Max
		if fd.Validations != nil && fd.Validations.MaxLength > 0 {
			bound = fd.Validations.MaxLength
		}
		var t string
		var ok bool
		if fd.Type == LongText {
			t, ok = prose(*a.Text, max(bound, 1))
		} else {
			t, ok = line(*a.Text, max(bound, 1))
		}
		if !ok {
			return nil, fmt.Sprintf("is at most %d characters", bound)
		}
		switch fd.Type {
		case Email:
			if !address(t) {
				return nil, "is not an email address"
			}
		case Phone:
			if !phoneShape.MatchString(t) || digits(t) < 3 || digits(t) > 20 {
				return nil, "is not a phone number"
			}
		case URL:
			u, err := url.Parse(t)
			if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
				return nil, "is not a web address"
			}
		}
		out.Text = &t
	case Number, Rating, OpinionScale:
		if s := only(a.Number != nil); s != "" {
			return nil, s + ": number"
		}
		n := *a.Number
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return nil, "is not a number"
		}
		switch fd.Type {
		case Rating:
			if n != math.Trunc(n) || n < 1 || n > float64(fd.Steps) {
				return nil, fmt.Sprintf("is a whole number from 1 to %d", fd.Steps)
			}
		case OpinionScale:
			lo, hi := float64(fd.Start), float64(fd.Start+fd.Steps-1)
			if n != math.Trunc(n) || n < lo || n > hi {
				return nil, fmt.Sprintf("is a whole number from %g to %g", lo, hi)
			}
		case Number:
			if v := fd.Validations; v != nil {
				if v.MinValue != nil && n < *v.MinValue {
					return nil, fmt.Sprintf("is at least %g", *v.MinValue)
				}
				if v.MaxValue != nil && n > *v.MaxValue {
					return nil, fmt.Sprintf("is at most %g", *v.MaxValue)
				}
			}
		}
		out.Number = &n
	case YesNo:
		if s := only(a.Boolean != nil); s != "" {
			return nil, s + ": boolean"
		}
		b := *a.Boolean
		out.Boolean = &b
	case MultiChoice, Dropdown:
		if s := only(len(a.Choices) > 0); s != "" {
			return nil, s + ": choices"
		}
		if (fd.Type == Dropdown || !fd.Multiple) && len(a.Choices) != 1 {
			return nil, "takes one choice"
		}
		var picked []string
		for _, c := range a.Choices {
			c = strings.TrimSpace(c)
			if !slices.ContainsFunc(fd.Choices, func(x Choice) bool { return x.Ref == c }) {
				return nil, fmt.Sprintf("offers no choice %q", c)
			}
			if slices.Contains(picked, c) {
				return nil, fmt.Sprintf("picks %q twice", c)
			}
			picked = append(picked, c)
		}
		out.Choices = picked
	case Date:
		if s := only(a.Date != ""); s != "" {
			return nil, s + ": date"
		}
		d := strings.TrimSpace(a.Date)
		if _, err := time.Parse(time.DateOnly, d); err != nil {
			return nil, "is a date, YYYY-MM-DD"
		}
		out.Date = d
	}
	return &out, ""
}

// digits counts the digits in s.
func digits(s string) int {
	n := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			n++
		}
	}
	return n
}

// said is an answer as one line of text a person reads: the text, the number,
// Yes or No, the labels of the choices picked, the date.
func said(fd Field, a Answer) string {
	switch {
	case a.Text != nil:
		return *a.Text
	case a.Number != nil:
		return strconv.FormatFloat(*a.Number, 'f', -1, 64)
	case a.Boolean != nil:
		if *a.Boolean {
			return "Yes"
		}
		return "No"
	case len(a.Choices) > 0:
		var labels []string
		for _, c := range a.Choices {
			for _, x := range fd.Choices {
				if x.Ref == c {
					labels = append(labels, x.Label)
				}
			}
		}
		return strings.Join(labels, ", ")
	}
	return a.Date
}

// textOf is the answer a ref names, as text; "" when it is unanswered.
func textOf(f Form, answers []Answer, ref string) string {
	fd, ok := fieldOf(f, ref)
	if !ok {
		return ""
	}
	for _, a := range answers {
		if a.Field == fd.Ref {
			return said(fd, a)
		}
	}
	return ""
}

// clip is s cut to n characters.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
