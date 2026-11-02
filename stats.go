package form

// stats.go — what a form's responses add up to: how many began and finished,
// and for each field how often it was answered, what was said, and how many
// respondents stopped in front of it.

import (
	"context"
	"strconv"
)

// SummaryIn asks for a form's summary.
type SummaryIn struct {
	// ID is the form's id or slug, from the path.
	ID string `json:"id"`
	// Since keeps the responses made at or after it, RFC 3339.
	Since string `json:"since"`
	// Until keeps the responses made before it, RFC 3339.
	Until string `json:"until"`
}

// Summary is what a form's responses add up to.
type Summary struct {
	// Form is the form's id.
	Form string `json:"form"`
	// Views is null: a view is stated on the event stream as form_viewed and
	// counted there, not kept with the responses.
	Views *int `json:"views"`
	// Starts is how many responses were begun: open and completed.
	Starts int `json:"starts"`
	// Completions is how many were completed.
	Completions int `json:"completions"`
	// CompletionRate is completions over starts, 0 to 1.
	CompletionRate float64 `json:"completion_rate"`
	// Scanned is how many responses were read; Truncated says the form has more
	// than one summary reads (50000), and the newest were read.
	Scanned   int  `json:"scanned"`
	Truncated bool `json:"truncated"`
	// Fields are each asked field's figures, in form order.
	Fields []FieldSummary `json:"fields"`
}

// FieldSummary is one field's figures.
type FieldSummary struct {
	// Field is the field's ref; Type its type; Title its question.
	Field string `json:"field"`
	Type  string `json:"type"`
	Title string `json:"title"`
	// Answered is how many responses answer it.
	Answered int `json:"answered"`
	// DropOff is how many open responses stopped in front of it: this is the
	// first field they left unanswered after the last one they answered.
	DropOff int `json:"drop_off"`
	// Distribution counts each value: every choice of a multiple_choice or
	// dropdown (with its label), every point of a rating or opinion_scale, and
	// yes and no.
	Distribution []Tally `json:"distribution,omitempty"`
	// Mean, Min and Max are a number, rating or opinion_scale's.
	Mean *float64 `json:"mean,omitempty"`
	Min  *float64 `json:"min,omitempty"`
	Max  *float64 `json:"max,omitempty"`
}

// Tally is one value and how many responses said it.
type Tally struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
	Count int    `json:"count"`
}

// summaryMost bounds how many responses one summary reads.
const summaryMost = 50000

// Sums up a form's responses: how many were begun and completed, and for each
// field how many answered it, the distribution of what was said (choices,
// points, yes and no), the mean, least and most of a number, and how many open
// responses stopped in front of it. since and until narrow it to when the
// responses were made. At most the newest 50000 are read, and truncated says so.
// An org admin only.
func (s *service) summary(ctx context.Context, in *SummaryIn) (*Summary, error) {
	org, f, err := s.adminForm(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	out := &Summary{Form: f.ID, Fields: []FieldSummary{}}
	type acc struct {
		sum, lo, hi float64
		n, k        int
		counts      map[string]int
	}
	accs := map[string]*acc{}
	var asked []Field
	for _, fd := range f.Fields {
		if fd.Type != Statement {
			asked = append(asked, fd)
			accs[fd.Ref] = &acc{counts: map[string]int{}}
		}
	}
	drop := map[string]int{}
	q := ResponsesIn{ID: f.ID, Since: in.Since, Until: in.Until, PageSize: 1000}
	for page := 1; out.Scanned < summaryMost; page++ {
		q.Page = page
		got, _, _, err := s.responses(ctx, org, f, q, 1000)
		if err != nil {
			return nil, err
		}
		for _, raw := range got.Items {
			r := responseOf(raw)
			out.Scanned++
			out.Starts++
			if r.Completed {
				out.Completions++
			}
			for _, a := range r.Answers {
				x := accs[a.Field]
				if x == nil {
					continue
				}
				x.n++
				switch {
				case a.Number != nil:
					// A stored value outside what a submission may say (written
					// before the bound, or by hand) is counted as answered and
					// left out of the figures, which stay finite.
					v := *a.Number
					if !bounded(v) {
						break
					}
					x.k++
					if x.k == 1 || v < x.lo {
						x.lo = v
					}
					if x.k == 1 || v > x.hi {
						x.hi = v
					}
					x.sum += v
					x.counts[strconv.FormatFloat(v, 'f', -1, 64)]++
				case a.Boolean != nil:
					x.counts[strconv.FormatBool(*a.Boolean)]++
				case len(a.Choices) > 0:
					for _, c := range a.Choices {
						x.counts[c]++
					}
				}
			}
			if !r.Completed {
				if next := after(asked, r.Answers); next != "" {
					drop[next]++
				}
			}
		}
		if len(got.Items) < 1000 {
			break
		}
		if out.Scanned >= summaryMost {
			out.Truncated = true
		}
	}
	if out.Starts > 0 {
		out.CompletionRate = float64(out.Completions) / float64(out.Starts)
	}
	for _, fd := range asked {
		x := accs[fd.Ref]
		fs := FieldSummary{Field: fd.Ref, Type: fd.Type, Title: fd.Title, Answered: x.n, DropOff: drop[fd.Ref]}
		switch fd.Type {
		case MultiChoice, Dropdown:
			for _, c := range fd.Choices {
				fs.Distribution = append(fs.Distribution, Tally{Value: c.Ref, Label: c.Label, Count: x.counts[c.Ref]})
			}
		case Rating, OpinionScale:
			lo, hi := 1, fd.Steps
			if fd.Type == OpinionScale {
				lo, hi = fd.Start, fd.Start+fd.Steps-1
			}
			for v := lo; v <= hi; v++ {
				fs.Distribution = append(fs.Distribution, Tally{Value: strconv.Itoa(v), Count: x.counts[strconv.Itoa(v)]})
			}
		case YesNo:
			fs.Distribution = []Tally{{Value: "true", Label: "Yes", Count: x.counts["true"]}, {Value: "false", Label: "No", Count: x.counts["false"]}}
		}
		if (fd.Type == Number || fd.Type == Rating || fd.Type == OpinionScale) && x.k > 0 {
			mean, lo, hi := x.sum/float64(x.k), x.lo, x.hi
			fs.Mean, fs.Min, fs.Max = &mean, &lo, &hi
		}
		out.Fields = append(out.Fields, fs)
	}
	return out, nil
}

// after is the field an open response stopped in front of: the first asked
// field after the last one it answered, or the first field when it answered none.
func after(asked []Field, answers []Answer) string {
	lastAt := -1
	for i, fd := range asked {
		for _, a := range answers {
			if a.Field == fd.Ref {
				lastAt = i
			}
		}
	}
	if lastAt+1 < len(asked) {
		return asked[lastAt+1].Ref
	}
	return ""
}
