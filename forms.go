package form

// forms.go — an org's forms: made, listed, read, replaced, changed and deleted
// by the org's admins.

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/zap-proto/zip"
)

// FormRef names one form of the caller's org.
type FormRef struct {
	// ID is the form's id or its slug, from the path.
	ID string `json:"id"`
}

// FormList is one page of an org's forms.
type FormList struct {
	// Items are the forms, newest first.
	Items []Form `json:"items"`
	// Page is this page, from 1.
	Page int `json:"page"`
	// PageSize is how many a page holds.
	PageSize int `json:"page_size"`
	// Total is how many forms answer the query.
	Total int `json:"total"`
}

// FormsIn asks for a page of the org's forms.
type FormsIn struct {
	// Page is the page, from 1. Default 1.
	Page int `json:"page"`
	// PageSize is how many a page holds: at most 200, default 50.
	PageSize int `json:"page_size"`
	// Search keeps the forms whose title holds it.
	Search string `json:"search"`
	// Tag keeps the forms tagged with it.
	Tag string `json:"tag"`
}

// FormPatch is a change to a form: every field present replaces the form's, every
// field absent keeps it.
type FormPatch struct {
	// ID is the form's id or its slug, from the path.
	ID       string   `json:"id"`
	Kind     *string  `json:"kind,omitempty"`
	Title    *string  `json:"title,omitempty"`
	Fields   []Field  `json:"fields,omitempty"`
	Welcome  *Screen  `json:"welcome,omitempty"`
	Thanks   *Screen  `json:"thanks,omitempty"`
	Hidden   []string `json:"hidden,omitempty"`
	Settings *Options `json:"settings,omitempty"`
	Theme    *string  `json:"theme,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Sinks    *Sinks   `json:"sinks,omitempty"`
}

// formKey is the record id a form's id or slug names.
func formKey(key string) string {
	key = strings.TrimSpace(key)
	if recordID(key) {
		return key
	}
	return derived("form", key)
}

// lookup reads the form key names, by id and then by slug.
func (s *service) lookup(ctx context.Context, org, key string) (*Form, error) {
	key = strings.TrimSpace(key)
	ids := []string{formKey(key)}
	if recordID(key) && slugShape.MatchString(key) {
		ids = append(ids, derived("form", key))
	}
	for _, id := range ids {
		r, err := s.d.Store.Get(ctx, org, Forms, id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, s.failed("the form could not be read", org, err)
		}
		f := formOf(org, r)
		return &f, nil
	}
	return nil, zip.ErrNotFound("form: no such form")
}

// formOf is a stored form as the wire carries it.
func formOf(org string, r Record) Form {
	var f Form
	decode(r, "doc", &f)
	f.ID = field(r, "id")
	f.Slug = field(r, "slug")
	f.PublicID = org + "." + publicName(f)
	f.Created, f.Updated = wire(r, "created"), wire(r, "updated")
	if f.Fields == nil {
		f.Fields = []Field{}
	}
	return f
}

// publicName is the part of a public id that names the form in its org.
func publicName(f Form) string {
	if f.Slug != "" {
		return f.Slug
	}
	return f.ID
}

// recordOf is a form as the store keeps it.
func recordOf(f Form) Record {
	doc := f
	doc.ID, doc.PublicID, doc.Created, doc.Updated = "", "", "", ""
	tags := f.Tags
	if tags == nil {
		tags = []string{}
	}
	return Record{"id": f.ID, "slug": f.Slug, "title": f.Title, "kind": f.Kind, "theme": f.Theme, "tags": tags, "doc": doc}
}

// save checks f against the platform and its theme and writes it.
func (s *service) save(ctx context.Context, org string, f Form, held *Form) (*Form, error) {
	f, problems := checkForm(f, s.settings(ctx), held)
	if len(problems) > 0 {
		return nil, zip.ErrBadRequest("form: " + strings.Join(problems, "; "))
	}
	if f.Theme != "" {
		if _, err := s.d.Store.Get(ctx, org, Themes, f.Theme); errors.Is(err, ErrNotFound) {
			return nil, zip.ErrBadRequest("form: theme names no theme of this org")
		} else if err != nil {
			return nil, s.failed("the theme could not be read", org, err)
		}
	}
	if err := s.d.Store.Put(ctx, org, Forms, recordOf(f)); err != nil {
		return nil, s.failed("the form could not be saved", org, err)
	}
	if err := s.publish(ctx, org, f, f.Settings.Visibility == "public"); err != nil {
		return nil, err
	}
	return s.lookup(ctx, org, f.ID)
}

// publish puts a form's public id in the index, or takes it out.
func (s *service) publish(ctx context.Context, org string, f Form, public bool) error {
	if err := s.indexReady(ctx); err != nil {
		return err
	}
	pid := org + "." + publicName(f)
	id := derived("public", pid)
	var err error
	if public {
		err = s.d.Store.Put(ctx, s.d.Platform, Index, Record{"id": id, "public": pid, "org": org, "form": f.ID})
	} else {
		err = s.d.Store.Delete(ctx, s.d.Platform, Index, id)
	}
	if err != nil {
		return s.failed("the form's public address could not be written", org, err)
	}
	return nil
}

// Makes a form in the caller's org and answers it as stored, its fields' ids
// assigned and its defaults filled: kind form, visibility public, language en,
// one question at a time, and the platform's default sinks when it names none.
//
// A slug, when given, is the form's id for good: a second form with the same
// slug is refused (409). Every problem with the form is named at once (400). An
// org admin only.
//
// Example: {"slug":"video-feedback","title":"How was the video?","fields":[{"ref":"rating","type":"rating","title":"Rate the video","required":true},{"ref":"comment","type":"long_text","title":"Anything to tell us?"}],"hidden":["video"]}
func (s *service) createForm(ctx context.Context, in *Form) (*Form, error) {
	org, err := s.change(ctx)
	if err != nil {
		return nil, err
	}
	f := *in
	f.Slug = strings.TrimSpace(f.Slug)
	if f.Slug != "" {
		f.ID = derived("form", f.Slug)
		if _, err := s.d.Store.Get(ctx, org, Forms, f.ID); err == nil {
			return nil, zip.Errorf(http.StatusConflict, "form: a form with slug %q exists", f.Slug)
		} else if !errors.Is(err, ErrNotFound) {
			return nil, s.failed("the form could not be read", org, err)
		}
	} else {
		f.ID = fresh()
	}
	return s.save(ctx, org, f, nil)
}

// Lists the caller org's forms, newest first, a page at a time; search keeps
// the forms whose title holds it and tag the ones carrying it. An org admin only.
func (s *service) listForms(ctx context.Context, in *FormsIn) (*FormList, error) {
	org, err := s.admin(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.ready(ctx, org); err != nil {
		return nil, err
	}
	page, size := paged(in.Page, in.PageSize, 50, 200)
	var where []Record
	if q := strings.TrimSpace(in.Search); q != "" {
		where = append(where, leaf("title", "contains", q))
	}
	if t := strings.TrimSpace(in.Tag); t != "" {
		where = append(where, leaf("tags", "contains", t))
	}
	got, err := s.d.Store.List(ctx, org, Forms, Query{Where: and(where...), Sort: "-created", Page: page, PerPage: size, Count: true})
	if err != nil {
		return nil, s.failed("the forms could not be read", org, err)
	}
	out := &FormList{Items: []Form{}, Page: page, PageSize: size, Total: got.Total}
	for _, r := range got.Items {
		out.Items = append(out.Items, formOf(org, r))
	}
	return out, nil
}

// Reads one form of the caller's org, by its id or its slug. An org admin only.
func (s *service) getForm(ctx context.Context, in *FormRef) (*Form, error) {
	org, err := s.admin(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.ready(ctx, org); err != nil {
		return nil, err
	}
	return s.lookup(ctx, org, in.ID)
}

// Replaces a form whole and answers it as stored. Named by a slug no form has
// yet, it makes the form under that slug, so a definition written twice is one
// form: the way a seed states a form. A field sent with its id keeps it; a type
// the platform no longer offers stays answerable where the form already asks
// it, and is refused where it is new. An org admin only.
func (s *service) replaceForm(ctx context.Context, in *Form) (*Form, error) {
	org, err := s.change(ctx)
	if err != nil {
		return nil, err
	}
	key := strings.TrimSpace(in.ID)
	held, err := s.lookup(ctx, org, key)
	var he *zip.HTTPError
	switch {
	case err == nil:
	case errors.As(err, &he) && he.Status == http.StatusNotFound && !recordID(key) && slugShape.MatchString(key):
		f := *in
		f.Slug, f.ID = key, derived("form", key)
		return s.save(ctx, org, f, nil)
	default:
		return nil, err
	}
	f := *in
	f.ID = held.ID
	if f.Slug == "" {
		f.Slug = held.Slug
	}
	return s.save(ctx, org, f, held)
}

// Changes the parts of a form the body names and keeps the rest, then answers it
// as stored. The whole form is checked as it will be, exactly as a replace is.
// An org admin only.
func (s *service) updateForm(ctx context.Context, in *FormPatch) (*Form, error) {
	org, err := s.change(ctx)
	if err != nil {
		return nil, err
	}
	held, err := s.lookup(ctx, org, in.ID)
	if err != nil {
		return nil, err
	}
	f := *held
	if in.Kind != nil {
		f.Kind = *in.Kind
	}
	if in.Title != nil {
		f.Title = *in.Title
	}
	if in.Fields != nil {
		f.Fields = in.Fields
	}
	if in.Welcome != nil {
		f.Welcome = in.Welcome
	}
	if in.Thanks != nil {
		f.Thanks = in.Thanks
	}
	if in.Hidden != nil {
		f.Hidden = in.Hidden
	}
	if in.Settings != nil {
		f.Settings = *in.Settings
	}
	if in.Theme != nil {
		f.Theme = *in.Theme
	}
	if in.Tags != nil {
		f.Tags = in.Tags
	}
	if in.Sinks != nil {
		f.Sinks = in.Sinks
	}
	return s.save(ctx, org, f, held)
}

// Deletes a form with every response, webhook and delivery it has, and the
// webhooks' signing secrets. An org admin only.
func (s *service) deleteForm(ctx context.Context, in *FormRef) (*struct{}, error) {
	org, err := s.change(ctx)
	if err != nil {
		return nil, err
	}
	f, err := s.lookup(ctx, org, in.ID)
	if err != nil {
		return nil, err
	}
	hooks, err := s.d.Store.List(ctx, org, Webhooks, Query{Where: leaf("form", "is", f.ID), PerPage: 100})
	if err != nil {
		return nil, s.failed("the form's webhooks could not be read", org, err)
	}
	for _, h := range hooks.Items {
		if err := s.forget(ctx, org, field(h, "id")); err != nil {
			return nil, err
		}
	}
	if err := s.publish(ctx, org, *f, false); err != nil {
		return nil, err
	}
	if err := s.d.Store.Delete(ctx, org, Forms, f.ID); err != nil {
		return nil, s.failed("the form could not be deleted", org, err)
	}
	return &struct{}{}, nil
}

// paged is a page and its size, each defaulted and bounded.
func paged(page, size, def, most int) (int, int) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = def
	}
	return page, min(size, most)
}
