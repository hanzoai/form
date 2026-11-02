package main

// import.go — commerce's forms into /v1/form. Each form is stated with a PUT on
// its slug, its records follow 500 to a batch through the form's import, and a
// cursor is written after every batch the service acknowledges, so a restart
// continues after the last one. Both writes are idempotent on the service.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/hanzoai/form"
)

// The commerce kinds a form's records are, in the order they are imported.
const (
	Subscriber = "subscriber"
	Submission = "submission"
)

var kinds = []string{Subscriber, Submission}

// batch is the most records one import carries.
const batch = 500

// Origin is one commerce form.
type Origin struct {
	ID   string
	Name string
	// Type is commerce's: subscribe or submit.
	Type string
	// Forward is the address commerce forwarded each record to; empty when off.
	Forward string
	// Recipient is who the forward address is.
	Recipient string
	// Thanks is commerce's thank-you type: html, redirect or disabled.
	Thanks string
	// URL is where a redirect thank-you sends the respondent.
	URL string
	// Welcome is whether commerce mailed a welcome.
	Welcome bool
}

// Row is one subscriber or submission.
type Row struct {
	ID       string
	At       time.Time
	Email    string
	Metadata map[string]any
}

// Source is where the forms and their records are read.
type Source interface {
	// Forms reads every form, in one stable order.
	Forms(fn func(Origin) error) error
	// Count answers how many records of kind the form holds; every record of
	// the kind when id is empty.
	Count(id, kind string) (int, error)
	// Rows reads every record of kind the form holds, in one stable order.
	Rows(id, kind string, fn func(Row) error) error
}

// mark is the cursor: the last record of the last acknowledged batch.
type mark struct {
	Source string `json:"source"`
	API    string `json:"api"`
	Form   string `json:"form"`
	Kind   string `json:"kind"`
	Row    string `json:"row"`
}

// importer moves one source into /v1/form.
type importer struct {
	src    Source
	name   string // the source, as the cursor records it
	api    *client
	cursor string
	file   bool
	out    io.Writer
	total  tally
}

// tally is what a run did.
type tally struct {
	forms, rows, dupes, created, existing, failed int
}

// run imports every form and its records, continuing after the cursor.
func (im *importer) run() error {
	at, err := im.load()
	if err != nil {
		return err
	}
	slugs := map[string]string{}
	err = im.src.Forms(func(o Origin) error {
		slug, err := slugOf(o.ID, slugs)
		if err != nil {
			return err
		}
		if at != nil && at.Form != o.ID {
			return nil
		}
		from := at
		at = nil
		return im.form(o, slug, from)
	})
	if err != nil {
		return err
	}
	if at != nil {
		return fmt.Errorf("the cursor %s names form %s, which the store does not hold", im.cursor, at.Form)
	}
	t := im.total
	fmt.Fprintf(im.out, "done: forms %d, records %d, duplicates skipped %d, created %d, existing %d, refused %d\n",
		t.forms, t.rows, t.dupes, t.created, t.existing, t.failed)
	if err := os.Remove(im.cursor); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if t.failed > 0 {
		return fmt.Errorf("%d records refused, each named above", t.failed)
	}
	return nil
}

// form states one form and imports its records, from the cursor when it names it.
func (im *importer) form(o Origin, slug string, from *mark) error {
	keys := map[string]bool{}
	for _, k := range kinds {
		err := im.src.Rows(o.ID, k, func(r Row) error {
			for key := range r.Metadata {
				keys[key] = true
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	f, refs := define(o, slug, keys)
	if o.Welcome {
		fmt.Fprintf(im.out, "%s: welcome mail not carried: commerce holds a provider template id, not a text body\n", slug)
	}
	if err := im.api.call("PUT", "/v1/form/forms/"+slug, f, nil); err != nil {
		return err
	}
	im.total.forms++
	for i, k := range kinds {
		after := ""
		if from != nil {
			switch j := slices.Index(kinds, from.Kind); {
			case i < j:
				continue
			case i == j:
				after = from.Row
			}
		}
		if err := im.rows(o, slug, f.Kind == form.Subscribe, k, refs, after); err != nil {
			return err
		}
	}
	return nil
}

// rows imports one kind of a form's records, after the row named, 500 at a time.
// On a subscribe form one address is one response, so a batch carries it once.
func (im *importer) rows(o Origin, slug string, subscribe bool, kind string, refs map[string]string, after string) error {
	skip := after != ""
	var out []form.Imported
	seen := map[string]bool{}
	last := ""
	send := func() error {
		if len(out) == 0 {
			return nil
		}
		var got form.Imports
		if err := im.api.call("POST", "/v1/form/forms/"+slug+"/import", form.ImportIn{ID: slug, Responses: out, File: im.file}, &got); err != nil {
			return err
		}
		im.total.created += got.Created
		im.total.existing += got.Existing
		im.total.failed += len(got.Failed)
		for _, f := range got.Failed {
			fmt.Fprintf(im.out, "%s: refused %s: %s\n", slug, f.External, f.Reason)
		}
		fmt.Fprintf(im.out, "%s: %s batch of %d: created %d, existing %d, refused %d\n", slug, kind, len(out), got.Created, got.Existing, len(got.Failed))
		if err := im.save(mark{Form: o.ID, Kind: kind, Row: last}); err != nil {
			return err
		}
		out = out[:0]
		clear(seen)
		return nil
	}
	err := im.src.Rows(o.ID, kind, func(r Row) error {
		if skip {
			skip = r.ID != after
			return nil
		}
		last = r.ID
		im.total.rows++
		in := imported(kind, r, refs)
		key := in.External
		if subscribe && in.Email != "" {
			key = in.Email
		}
		if seen[key] {
			im.total.dupes++
			return nil
		}
		seen[key] = true
		out = append(out, in)
		if len(out) == batch {
			return send()
		}
		return nil
	})
	if err != nil {
		return err
	}
	if skip {
		return fmt.Errorf("the cursor's %s %s is gone from form %s; remove %s to start over", kind, after, o.ID, im.cursor)
	}
	return send()
}

// count says, per form, how many records an import would read, and how many
// records name no form at all and so are not carried. Nothing is written.
func count(src Source, out io.Writer) error {
	slugs := map[string]string{}
	sum := map[string]int{}
	forms := 0
	err := src.Forms(func(o Origin) error {
		slug, err := slugOf(o.ID, slugs)
		if err != nil {
			return err
		}
		forms++
		kind := form.Plain
		if o.Type == "subscribe" {
			kind = form.Subscribe
		}
		fmt.Fprintf(out, "%s\t%s\t%q", slug, kind, o.Name)
		for _, k := range kinds {
			n, err := src.Count(o.ID, k)
			if err != nil {
				return err
			}
			sum[k] += n
			fmt.Fprintf(out, "\t%s %d", k, n)
		}
		fmt.Fprintln(out)
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "total: forms %d", forms)
	for _, k := range kinds {
		all, err := src.Count("", k)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, ", %s %d (%d name no form)", k, sum[k], all-sum[k])
	}
	fmt.Fprintln(out)
	return nil
}

// slugShape is the service's slug.
var slugShape = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)

// slugOf is the slug a commerce form becomes, "commerce-<id>" lowercased. seen
// holds the slugs already given, so two ids that lowercase alike are refused
// rather than made one form.
func slugOf(id string, seen map[string]string) (string, error) {
	slug := strings.ToLower("commerce-" + id)
	if !slugShape.MatchString(slug) {
		return "", fmt.Errorf("form %s makes no valid slug", id)
	}
	if prev, ok := seen[slug]; ok {
		return "", fmt.Errorf("forms %s and %s both become %s", prev, id, slug)
	}
	seen[slug] = id
	return slug, nil
}

// define is the form an origin becomes, and the field ref of each metadata key:
// an email field (required on a subscribe form) and a short_text field per key.
func define(o Origin, slug string, keys map[string]bool) (form.Form, map[string]string) {
	f := form.Form{
		ID: slug, Slug: slug, Kind: form.Plain, Title: o.Name,
		Fields: []form.Field{{Ref: "email", Type: form.Email, Title: "Email"}},
		Sinks:  &form.Sinks{},
	}
	if o.Type == "subscribe" {
		f.Kind = form.Subscribe
		f.Fields[0].Required = true
		f.Sinks.CRM = &form.CRM{List: o.Name}
	}
	if o.Forward != "" {
		f.Sinks.Forward = &form.Forward{Email: o.Forward, Name: o.Recipient}
	}
	if u, err := url.Parse(o.URL); o.Thanks == "redirect" && err == nil && u.Scheme == "https" && u.Host != "" {
		f.Thanks = &form.Screen{Redirect: o.URL}
	}
	refs := map[string]string{}
	taken := map[string]bool{"email": true}
	for _, k := range slices.Sorted(maps.Keys(keys)) {
		ref := refOf(k, taken)
		refs[k] = ref
		title := strings.TrimSpace(k)
		if title == "" {
			title = ref
		}
		f.Fields = append(f.Fields, form.Field{Ref: ref, Type: form.ShortText, Title: title})
	}
	return f, refs
}

// refOf is a metadata key as a field ref no other field has: lowercase letters,
// digits, dash and underscore, every other character an underscore.
func refOf(key string, taken map[string]bool) string {
	b := []byte{}
	for _, r := range strings.ToLower(strings.TrimSpace(key)) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			b = append(b, byte(r))
		} else {
			b = append(b, '_')
		}
	}
	base := string(b[:min(len(b), 56)])
	if base == "" {
		base = "field"
	}
	ref := base
	for n := 2; taken[ref]; n++ {
		ref = fmt.Sprintf("%s_%d", base, n)
	}
	taken[ref] = true
	return ref
}

// imported is a record as the service imports it.
func imported(kind string, r Row, refs map[string]string) form.Imported {
	in := form.Imported{External: kind + "_" + r.ID, Email: strings.ToLower(strings.TrimSpace(r.Email)), Source: "commerce"}
	if !r.At.IsZero() {
		in.At = r.At.UTC().Format(time.RFC3339)
	}
	for _, k := range slices.Sorted(maps.Keys(r.Metadata)) {
		var s string
		switch v := r.Metadata[k].(type) {
		case nil:
			continue
		case string:
			s = v
		default:
			b, err := json.Marshal(v)
			if err != nil {
				continue
			}
			s = string(b)
		}
		in.Answers = append(in.Answers, form.Answer{Field: refs[k], Text: &s})
	}
	return in
}

// load reads the cursor, nil when there is none. A cursor made against another
// source or API is refused.
func (im *importer) load() (*mark, error) {
	b, err := os.ReadFile(im.cursor)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m mark
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("cursor %s: %w", im.cursor, err)
	}
	if m.Source != im.name || m.API != im.api.base {
		return nil, fmt.Errorf("cursor %s was written for %s at %s; name another -cursor", im.cursor, m.Source, m.API)
	}
	return &m, nil
}

// save writes the cursor whole, or not at all.
func (im *importer) save(m mark) error {
	m.Source, m.API = im.name, im.api.base
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp := im.cursor + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, im.cursor)
}
