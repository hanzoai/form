package form

// responses.go — a form's responses, for the org's admins: listed under a
// filter, read, deleted, exported whole, and brought in from elsewhere.

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"strings"
	"time"

	"github.com/zap-proto/zip"
)

// ResponsesIn asks for a page of a form's responses.
type ResponsesIn struct {
	// ID is the form's id or slug, from the path.
	ID string `json:"id"`
	// Page is the page, from 1. Default 1.
	Page int `json:"page"`
	// PageSize is how many a page holds: at most 1000, default 25.
	PageSize int `json:"page_size"`
	// Since keeps the responses made at or after it, RFC 3339.
	Since string `json:"since"`
	// Until keeps the responses made before it, RFC 3339.
	Until string `json:"until"`
	// Completed keeps the completed responses (true) or the open ones (false);
	// absent keeps both.
	Completed *bool `json:"completed"`
	// Query keeps the responses whose answers hold it.
	Query string `json:"query"`
	// Sort is submitted_at,desc (the default) or submitted_at,asc.
	Sort string `json:"sort"`
	// IncludedResponseIDs keeps only these responses, comma separated, at most 90.
	IncludedResponseIDs string `json:"included_response_ids"`
	// ExcludedResponseIDs drops these responses, comma separated, at most 90.
	ExcludedResponseIDs string `json:"excluded_response_ids"`
}

// ResponseList is one page of a form's responses.
type ResponseList struct {
	// Items are the responses.
	Items []Response `json:"items"`
	// Page is this page, from 1.
	Page int `json:"page"`
	// PageSize is how many a page holds.
	PageSize int `json:"page_size"`
	// Total is how many responses answer the filter.
	Total int `json:"total"`
}

// ResponseRef names one response of one form.
type ResponseRef struct {
	// ID is the form's id or slug, from the path.
	ID string `json:"id"`
	// Response is the response's id, from the path.
	Response string `json:"response"`
}

// ExportIn asks for a form's responses whole, a page at a time.
type ExportIn struct {
	ResponsesIn
	// Format is json (the default) or csv.
	Format string `json:"format"`
}

// Export is a page of a form's responses as a file.
type Export struct {
	// Format is json or csv.
	Format string `json:"format"`
	// Filename is what to save it as.
	Filename string `json:"filename"`
	// Items are the responses, for json.
	Items []Response `json:"items,omitempty"`
	// CSV is the responses as CSV, for csv: one row each, one column per field,
	// then the hidden values, the respondent and the times.
	CSV string `json:"csv,omitempty"`
	// Page is this page; Next the page after it, 0 when this is the last.
	Page int `json:"page"`
	Next int `json:"next"`
	// Total is how many responses answer the filter.
	Total int `json:"total"`
}

// responseOf is a stored response as the wire carries it.
func responseOf(r Record) Response {
	out := Response{
		ID: field(r, "id"), Form: field(r, "form"), Token: field(r, "token"),
		LandedAt: wire(r, "landed"), SubmittedAt: wire(r, "submitted"),
		Path: field(r, "path"), Source: field(r, "source"), External: field(r, "external"),
		Created: wire(r, "at"), Updated: wire(r, "updated"),
	}
	out.Completed, _ = r["completed"].(bool)
	if d, ok := r["duration"].(float64); ok {
		out.Duration = int(d)
	}
	decode(r, "answers", &out.Answers)
	decode(r, "hidden", &out.Hidden)
	decode(r, "respondent", &out.Respondent)
	if out.Answers == nil {
		out.Answers = []Answer{}
	}
	return out
}

// filter is the where clause a listing's query asks.
func filter(form string, in ResponsesIn) (Record, error) {
	c := []Record{leaf("form", "is", form)}
	for _, b := range []struct {
		v, op string
	}{{in.Since, "gte"}, {in.Until, "lt"}} {
		if strings.TrimSpace(b.v) == "" {
			continue
		}
		t := when(b.v)
		if t.IsZero() {
			return nil, zip.ErrBadRequest("form: since and until are RFC 3339 times")
		}
		c = append(c, leaf("at", b.op, baseTime(t)))
	}
	if in.Completed != nil {
		c = append(c, leaf("completed", "is", *in.Completed))
	}
	if q := strings.TrimSpace(in.Query); q != "" {
		if len(q) > 200 {
			return nil, zip.ErrBadRequest("form: query is at most 200 characters")
		}
		c = append(c, leaf("answers", "contains", q))
	}
	for _, set := range []struct {
		list, op string
		join     func(...Record) Record
	}{{in.IncludedResponseIDs, "is", or}, {in.ExcludedResponseIDs, "isNot", and}} {
		var ids []Record
		for _, id := range strings.Split(set.list, ",") {
			if id = strings.TrimSpace(id); id == "" {
				continue
			}
			if !recordID(id) {
				return nil, zip.ErrBadRequest("form: a response id is 15 lowercase letters and digits")
			}
			ids = append(ids, leaf("id", set.op, id))
		}
		if len(ids) > 90 {
			return nil, zip.ErrBadRequest("form: at most 90 response ids")
		}
		if len(ids) > 0 {
			c = append(c, set.join(ids...))
		}
	}
	return and(c...), nil
}

// order is a listing's sort.
func order(sort string) (string, error) {
	switch strings.TrimSpace(sort) {
	case "", "submitted_at,desc":
		return "-at,-id", nil
	case "submitted_at,asc":
		return "at,id", nil
	}
	return "", zip.ErrBadRequest("form: sort is submitted_at,desc or submitted_at,asc")
}

// responses reads one page of a form's responses under a listing's query.
func (s *service) responses(ctx context.Context, org string, f *Form, in ResponsesIn, most int) (*Page, int, int, error) {
	where, err := filter(f.ID, in)
	if err != nil {
		return nil, 0, 0, err
	}
	sort, err := order(in.Sort)
	if err != nil {
		return nil, 0, 0, err
	}
	page, size := paged(in.Page, in.PageSize, 25, most)
	got, err := s.d.Store.List(ctx, org, Responses, Query{Where: where, Sort: sort, Page: page, PerPage: size, Count: true})
	if err != nil {
		return nil, 0, 0, s.failed("the responses could not be read", org, err)
	}
	return got, page, size, nil
}

// adminForm admits an org admin and reads the form key names.
func (s *service) adminForm(ctx context.Context, key string) (string, *Form, error) {
	org, err := s.admin(ctx)
	if err != nil {
		return "", nil, err
	}
	if err := s.ready(ctx, org); err != nil {
		return "", nil, err
	}
	f, err := s.lookup(ctx, org, key)
	if err != nil {
		return "", nil, err
	}
	return org, f, nil
}

// Lists a form's responses, newest first, a page at a time: since and until
// bound when they were made, completed keeps the finished or the open ones, query
// keeps the ones whose answers hold it, and included_response_ids and
// excluded_response_ids keep or drop named ones. An org admin only.
func (s *service) listResponses(ctx context.Context, in *ResponsesIn) (*ResponseList, error) {
	org, f, err := s.adminForm(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	got, page, size, err := s.responses(ctx, org, f, *in, 1000)
	if err != nil {
		return nil, err
	}
	out := &ResponseList{Items: []Response{}, Page: page, PageSize: size, Total: got.Total}
	for _, r := range got.Items {
		out.Items = append(out.Items, responseOf(r))
	}
	s.expire(org, f.ID, s.settings(ctx))
	return out, nil
}

// Reads one response of a form. An org admin only.
func (s *service) getResponse(ctx context.Context, in *ResponseRef) (*Response, error) {
	org, f, err := s.adminForm(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	r, err := s.responseAt(ctx, org, f.ID, in.Response)
	if err != nil {
		return nil, err
	}
	out := responseOf(r)
	return &out, nil
}

// responseAt reads a response of form, 404 when the form has none by that id.
func (s *service) responseAt(ctx context.Context, org, form, id string) (Record, error) {
	if !recordID(strings.TrimSpace(id)) {
		return nil, zip.ErrNotFound("form: no such response")
	}
	r, err := s.d.Store.Get(ctx, org, Responses, strings.TrimSpace(id))
	if errors.Is(err, ErrNotFound) || err == nil && field(r, "form") != form {
		return nil, zip.ErrNotFound("form: no such response")
	}
	if err != nil {
		return nil, s.failed("the response could not be read", org, err)
	}
	return r, nil
}

// Deletes one response of a form, and its webhook deliveries. What the sinks
// already hold (the CRM's timeline, a webhook's receiver) is theirs. An org admin
// only.
func (s *service) deleteResponse(ctx context.Context, in *ResponseRef) (*struct{}, error) {
	if _, err := s.change(ctx); err != nil {
		return nil, err
	}
	org, f, err := s.adminForm(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	r, err := s.responseAt(ctx, org, f.ID, in.Response)
	if err != nil {
		return nil, err
	}
	id := field(r, "id")
	sent, err := s.d.Store.List(ctx, org, Deliveries, Query{Where: leaf("response", "is", id), PerPage: 100})
	if err != nil {
		return nil, s.failed("the response's deliveries could not be read", org, err)
	}
	var ids []string
	for _, d := range sent.Items {
		ids = append(ids, field(d, "id"))
	}
	if len(ids) > 0 {
		if err := s.d.Store.Delete(ctx, org, Deliveries, ids...); err != nil {
			return nil, s.failed("the response's deliveries could not be deleted", org, err)
		}
	}
	if err := s.d.Store.Delete(ctx, org, Responses, id); err != nil {
		return nil, s.failed("the response could not be deleted", org, err)
	}
	return &struct{}{}, nil
}

// Exports a form's responses as JSON or CSV, a page of up to 5000 at a time
// under the same filter a listing takes; next is the page to ask for after it,
// 0 at the end. The CSV has one column per field (its title), then each hidden
// value, the respondent's address and kind, and when the response was landed on
// and submitted. An org admin only.
func (s *service) export(ctx context.Context, in *ExportIn) (*Export, error) {
	org, f, err := s.adminForm(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	format := strings.TrimSpace(in.Format)
	if format == "" {
		format = "json"
	}
	if format != "json" && format != "csv" {
		return nil, zip.ErrBadRequest("form: format is json or csv")
	}
	if in.PageSize == 0 {
		in.PageSize = 1000
	}
	got, page, size, err := s.responses(ctx, org, f, in.ResponsesIn, 5000)
	if err != nil {
		return nil, err
	}
	out := &Export{Format: format, Filename: publicName(*f) + "-responses-" + time.Now().UTC().Format("20060102") + "." + format, Page: page, Total: got.Total}
	if page*size < got.Total {
		out.Next = page + 1
	}
	var rs []Response
	for _, r := range got.Items {
		rs = append(rs, responseOf(r))
	}
	if format == "json" {
		out.Items = rs
		if out.Items == nil {
			out.Items = []Response{}
		}
		return out, nil
	}
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	head := []string{"id"}
	var asked []Field
	for _, fd := range f.Fields {
		if fd.Type != Statement {
			asked = append(asked, fd)
			head = append(head, fd.Title)
		}
	}
	head = append(head, f.Hidden...)
	head = append(head, "email", "respondent", "completed", "landed_at", "submitted_at")
	_ = w.Write(head)
	for _, r := range rs {
		row := []string{r.ID}
		for _, fd := range asked {
			row = append(row, cell(textOf(*f, r.Answers, fd.Ref)))
		}
		for _, h := range f.Hidden {
			row = append(row, cell(r.Hidden[h]))
		}
		done := "false"
		if r.Completed {
			done = "true"
		}
		row = append(row, cell(r.Respondent.Email), r.Respondent.Kind, done, r.LandedAt, r.SubmittedAt)
		_ = w.Write(row)
	}
	w.Flush()
	out.CSV = b.String()
	return out, nil
}

// cell keeps a respondent's text from reading as a formula when the CSV is
// opened in a spreadsheet.
func cell(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}
