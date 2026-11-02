package form

// themes.go — an org's themes: how its forms are drawn.

import (
	"context"
	"errors"
	"strings"

	"github.com/zap-proto/zip"
)

// ThemeRef names one theme of the caller's org.
type ThemeRef struct {
	// ID is the theme's id, from the path.
	ID string `json:"id"`
}

// ThemeList is the org's themes.
type ThemeList struct {
	// Items are the themes, by name.
	Items []Theme `json:"items"`
}

// themeOf is a stored theme as the wire carries it.
func themeOf(r Record) Theme {
	var t Theme
	decode(r, "doc", &t)
	t.ID, t.Name = field(r, "id"), field(r, "name")
	t.Created, t.Updated = wire(r, "created"), wire(r, "updated")
	return t
}

// putTheme checks t and writes it under id.
func (s *service) putTheme(ctx context.Context, org, id string, t Theme) (*Theme, error) {
	t, problems := checkTheme(t)
	if len(problems) > 0 {
		return nil, zip.ErrBadRequest("theme: " + strings.Join(problems, "; "))
	}
	t.ID = id
	doc := t
	doc.ID, doc.Created, doc.Updated = "", "", ""
	if err := s.d.Store.Put(ctx, org, Themes, Record{"id": id, "name": t.Name, "doc": doc}); err != nil {
		return nil, s.failed("the theme could not be saved", org, err)
	}
	r, err := s.d.Store.Get(ctx, org, Themes, id)
	if err != nil {
		return nil, s.failed("the theme could not be read", org, err)
	}
	out := themeOf(r)
	return &out, nil
}

// themeAt reads the theme id names, 404 when the org has none.
func (s *service) themeAt(ctx context.Context, org, id string) (*Theme, error) {
	if !recordID(strings.TrimSpace(id)) {
		return nil, zip.ErrNotFound("theme: no such theme")
	}
	r, err := s.d.Store.Get(ctx, org, Themes, strings.TrimSpace(id))
	if errors.Is(err, ErrNotFound) {
		return nil, zip.ErrNotFound("theme: no such theme")
	}
	if err != nil {
		return nil, s.failed("the theme could not be read", org, err)
	}
	t := themeOf(r)
	return &t, nil
}

// Makes a theme in the caller's org: a name, four colours (#rrggbb), a font
// family and an https background image, each optional but the name. An org
// admin only.
//
// Example: {"name":"Hanzo dark","colors":{"question":"#ffffff","answer":"#f59e0b","button":"#ff6a00","background":"#0a0b0e"},"font":"Inter"}
func (s *service) createTheme(ctx context.Context, in *Theme) (*Theme, error) {
	org, err := s.change(ctx)
	if err != nil {
		return nil, err
	}
	return s.putTheme(ctx, org, fresh(), *in)
}

// Lists the caller org's themes by name. An org admin only.
func (s *service) listThemes(ctx context.Context, _ *struct{}) (*ThemeList, error) {
	org, err := s.admin(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.ready(ctx, org); err != nil {
		return nil, err
	}
	got, err := s.d.Store.List(ctx, org, Themes, Query{Sort: "name", PerPage: 500})
	if err != nil {
		return nil, s.failed("the themes could not be read", org, err)
	}
	out := &ThemeList{Items: []Theme{}}
	for _, r := range got.Items {
		out.Items = append(out.Items, themeOf(r))
	}
	return out, nil
}

// Reads one theme of the caller's org. An org admin only.
func (s *service) getTheme(ctx context.Context, in *ThemeRef) (*Theme, error) {
	org, err := s.admin(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.ready(ctx, org); err != nil {
		return nil, err
	}
	return s.themeAt(ctx, org, in.ID)
}

// Replaces a theme whole; every form drawn in it is drawn in the new one. An
// org admin only.
func (s *service) replaceTheme(ctx context.Context, in *Theme) (*Theme, error) {
	org, err := s.change(ctx)
	if err != nil {
		return nil, err
	}
	held, err := s.themeAt(ctx, org, in.ID)
	if err != nil {
		return nil, err
	}
	return s.putTheme(ctx, org, held.ID, *in)
}

// Deletes a theme. A form drawn in it is refused (409) until it names another,
// so no form is left naming a theme that is gone. An org admin only.
func (s *service) deleteTheme(ctx context.Context, in *ThemeRef) (*struct{}, error) {
	org, err := s.change(ctx)
	if err != nil {
		return nil, err
	}
	t, err := s.themeAt(ctx, org, in.ID)
	if err != nil {
		return nil, err
	}
	using, err := s.d.Store.List(ctx, org, Forms, Query{Where: leaf("theme", "is", t.ID), PerPage: 1, Count: true})
	if err != nil {
		return nil, s.failed("the forms could not be read", org, err)
	}
	if using.Total > 0 {
		return nil, zip.Errorf(409, "theme: %d form(s) are drawn in it", using.Total)
	}
	if err := s.d.Store.Delete(ctx, org, Themes, t.ID); err != nil {
		return nil, s.failed("the theme could not be deleted", org, err)
	}
	return &struct{}{}, nil
}
