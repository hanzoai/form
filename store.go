package form

// store.go — where the records live, and the shape they live in.
//
// Every record is a record of the org's own Base: its forms, themes, responses,
// webhooks and their deliveries are five collections this file declares, grown
// into the org's Base the first time the org writes one (Install: what is missing
// is created and nothing that exists changes). The host implements Store; the
// service never opens a database.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

// Record is one record's fields, as JSON gives them.
type Record = map[string]any

// Query asks for a page of one collection.
type Query struct {
	// Where is a Base where clause: {"and": [...]}, {"or": [...]} or a leaf
	// {"field": "...", "op": "is", "value": ...}. Every value is bound, never
	// spliced into filter text.
	Where any
	// Sort is Base's sort expression, "-submitted,id".
	Sort string
	// Page is 1-based; PerPage at most 1000.
	Page, PerPage int
	// Count asks for Total; without it Total is -1.
	Count bool
}

// Page is one page of a collection.
type Page struct {
	Items []Record
	// Total counts the whole filtered collection, or is -1 when not asked.
	Total int
}

// Store is one Base per org.
type Store interface {
	// Install grows org's Base by the collections given (Base's import shape).
	Install(ctx context.Context, org string, collections json.RawMessage) error
	// Get reads one record; ErrNotFound when there is none.
	Get(ctx context.Context, org, collection, id string) (Record, error)
	// List reads a page of one collection.
	List(ctx context.Context, org, collection string, q Query) (*Page, error)
	// Put creates each record or, when one with its id exists, sets the fields it names.
	Put(ctx context.Context, org, collection string, records ...Record) error
	// Delete removes the records by id; one already gone is not an error.
	Delete(ctx context.Context, org, collection string, ids ...string) error
}

// ErrNotFound is a record the store does not hold.
var ErrNotFound = errors.New("form: no such record")

// The collections, by name.
const (
	Forms      = "forms"
	Themes     = "form_themes"
	Responses  = "form_responses"
	Webhooks   = "form_webhooks"
	Deliveries = "form_deliveries"
)

// admins is the rule every collection reads and writes under in Base's own
// records API: an org's admins. The service itself writes as the org.
const admins = `@request.auth.admin = true`

// collections is the schema, in Base's import shape.
var collections = func() json.RawMessage {
	text := func(name string, max int) Record { return Record{"name": name, "type": "text", "max": max} }
	doc := func(name string) Record { return Record{"name": name, "type": "json", "maxSize": 2 << 20} }
	stamps := []Record{
		{"name": "created", "type": "autodate", "onCreate": true},
		{"name": "updated", "type": "autodate", "onCreate": true, "onUpdate": true},
	}
	of := func(name string, fields []Record, indexes ...string) Record {
		return Record{
			"name": name, "type": "base",
			"listRule": admins, "viewRule": admins, "createRule": admins, "updateRule": admins, "deleteRule": admins,
			"fields": append(fields, stamps...), "indexes": indexes,
		}
	}
	rel := func(name, to string) Record {
		return Record{"name": name, "type": "relation", "collectionId": to, "maxSelect": 1, "cascadeDelete": true, "required": true}
	}
	all := []Record{
		of(Forms, []Record{
			text("slug", 64), text("title", 200), text("kind", 32), text("theme", 15), doc("tags"), doc("doc"),
		}, "CREATE INDEX `idx_forms_slug` ON `forms` (`slug`)"),
		of(Themes, []Record{text("name", 100), doc("doc")}),
		of(Responses, []Record{
			rel("form", Forms), text("token", 64), {"name": "completed", "type": "bool"},
			doc("answers"), doc("hidden"), doc("respondent"), text("email", 320), text("person", 15),
			{"name": "at", "type": "date"}, {"name": "landed", "type": "date"}, {"name": "submitted", "type": "date"}, {"name": "duration", "type": "number"},
			text("last", 64), text("path", 500), text("source", 16), text("external", 200),
		}, "CREATE INDEX `idx_form_responses_form_at` ON `form_responses` (`form`, `at`)"),
		of(Webhooks, []Record{rel("form", Forms), text("tag", 64), text("url", 2000), {"name": "enabled", "type": "bool"}}),
		of(Deliveries, []Record{
			rel("webhook", Webhooks), text("form", 15), text("response", 15), text("status", 16),
			{"name": "attempts", "type": "number"}, {"name": "code", "type": "number"}, text("error", 500), {"name": "next", "type": "date"},
		}, "CREATE INDEX `idx_form_deliveries_webhook` ON `form_deliveries` (`webhook`, `created`)"),
	}
	b, err := json.Marshal(all)
	if err != nil {
		panic(err)
	}
	return b
}()

// Index is the one collection outside an org: in the platform org's Base
// (Deps.Platform), the public ids of every public form and the org each is in.
// It is how an address names an org without a request reaching into an org's
// Base that does not exist: a public id the index does not hold is a 404 that
// opened nothing.
const Index = "form_index"

// indexCollection is the index's schema, in Base's import shape.
var indexCollection = func() json.RawMessage {
	b, err := json.Marshal([]Record{{
		"name": Index, "type": "base",
		"listRule": admins, "viewRule": admins, "createRule": admins, "updateRule": admins, "deleteRule": admins,
		"fields": []Record{
			{"name": "public", "type": "text", "max": 200},
			{"name": "org", "type": "text", "max": 200},
			{"name": "form", "type": "text", "max": 15},
			{"name": "created", "type": "autodate", "onCreate": true},
			{"name": "updated", "type": "autodate", "onCreate": true, "onUpdate": true},
		},
	}})
	if err != nil {
		panic(err)
	}
	return b
}()

// indexReady grows the platform org's Base by the index, once per process.
func (s *service) indexReady(ctx context.Context) error {
	key := "\x00index"
	if _, ok := s.org.done.Load(key); ok {
		return nil
	}
	if err := s.d.Store.Install(ctx, s.d.Platform, indexCollection); err != nil {
		return s.failed("the public index could not be held", s.d.Platform, err)
	}
	s.org.done.Store(key, true)
	return nil
}

// installs remembers which orgs this process has grown, so a write asks once.
type installs struct{ done sync.Map }

// ready grows org's Base by the collections, once per process.
func (s *service) ready(ctx context.Context, org string) error {
	if _, ok := s.org.done.Load(org); ok {
		return nil
	}
	if err := s.d.Store.Install(ctx, org, collections); err != nil {
		return s.failed("the org's store could not hold forms", org, err)
	}
	s.org.done.Store(org, true)
	return nil
}

// derived is a record id derived from what the record is: 15 hex characters of
// the SHA-256 of its kind and key, inside Base's id shape.
func derived(kind, key string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + key))
	return hex.EncodeToString(sum[:])[:15]
}

// fresh is a new random record id in Base's id shape.
func fresh() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	var b [15]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b[:])
}

// recordID reports whether s has a record id's shape.
func recordID(s string) bool {
	if len(s) != 15 {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// field reads one field of a record as text.
func field(r Record, name string) string {
	v, _ := r[name].(string)
	return v
}

// decode reads a json field of a record into v.
func decode(r Record, name string, v any) {
	switch x := r[name].(type) {
	case nil:
	case string:
		_ = json.Unmarshal([]byte(x), v)
	default:
		b, _ := json.Marshal(x)
		_ = json.Unmarshal(b, v)
	}
}

// baseTime is a time as Base stores a date.
func baseTime(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05.000Z") }

// when reads a date a record or a caller gave: Base's own shape or RFC 3339.
func when(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"2006-01-02 15:04:05.000Z", "2006-01-02 15:04:05Z", time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// wire is a record's date as the wire carries it.
func wire(r Record, name string) string { return stamp(when(field(r, name))) }

// leaf is one condition of a where clause.
func leaf(name, op string, value any) Record { return Record{"field": name, "op": op, "value": value} }

// and joins conditions.
func and(c ...Record) Record { return Record{"and": c} }

// or joins alternatives.
func or(c ...Record) Record { return Record{"or": c} }
