package form

// memory_test.go — a Store held in memory for this package's tests: records
// round-trip through JSON as Base's do, dates are Base's text, and a where clause
// is read with the operators this package writes.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type memory struct {
	mu        sync.Mutex
	installed map[string]bool
	rows      map[string]map[string]Record // org\x00collection -> id -> record
	puts      int
}

func newMemory() *memory {
	return &memory{installed: map[string]bool{}, rows: map[string]map[string]Record{}}
}

func (m *memory) table(org, c string) map[string]Record {
	k := org + "\x00" + c
	if m.rows[k] == nil {
		m.rows[k] = map[string]Record{}
	}
	return m.rows[k]
}

func (m *memory) Install(_ context.Context, org string, collections json.RawMessage) error {
	var cs []Record
	if err := json.Unmarshal(collections, &cs); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range cs {
		m.installed[org+"\x00"+c["name"].(string)] = true
	}
	return nil
}

func (m *memory) check(org, c string) error {
	if !m.installed[org+"\x00"+c] {
		return fmt.Errorf("memory: %s has no collection %s", org, c)
	}
	return nil
}

func (m *memory) Get(_ context.Context, org, c, id string) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.check(org, c); err != nil {
		return nil, err
	}
	r, ok := m.table(org, c)[id]
	if !ok {
		return nil, ErrNotFound
	}
	return clone(r), nil
}

func (m *memory) Put(_ context.Context, org, c string, rs ...Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.check(org, c); err != nil {
		return err
	}
	m.puts++
	now := time.Now().UTC().Format("2006-01-02 15:04:05.000Z")
	t := m.table(org, c)
	for _, r := range rs {
		r = clone(r)
		id, _ := r["id"].(string)
		held, ok := t[id]
		if !ok {
			held = Record{"id": id, "created": now}
		}
		for k, v := range r {
			held[k] = v
		}
		held["updated"] = now
		t[id] = held
	}
	return nil
}

func (m *memory) Delete(_ context.Context, org, c string, ids ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.check(org, c); err != nil {
		return err
	}
	t := m.table(org, c)
	for _, id := range ids {
		delete(t, id)
		// The relations cascade as Base's do.
		if c == Forms {
			for _, child := range []string{Responses, Webhooks} {
				for k, r := range m.table(org, child) {
					if r["form"] == id {
						delete(m.table(org, child), k)
					}
				}
			}
		}
	}
	return nil
}

func (m *memory) List(_ context.Context, org, c string, q Query) (*Page, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.check(org, c); err != nil {
		return nil, err
	}
	var where Record
	if q.Where != nil {
		b, _ := json.Marshal(q.Where)
		_ = json.Unmarshal(b, &where)
	}
	var out []Record
	for _, r := range m.table(org, c) {
		if matches(r, where) {
			out = append(out, clone(r))
		}
	}
	keys := strings.Split(q.Sort, ",")
	sort.SliceStable(out, func(i, j int) bool {
		for _, k := range keys {
			k = strings.TrimSpace(k)
			if k == "" {
				continue
			}
			desc := strings.HasPrefix(k, "-")
			k = strings.TrimPrefix(k, "-")
			a, b := fmt.Sprint(out[i][k]), fmt.Sprint(out[j][k])
			if a != b {
				return (a < b) != desc
			}
		}
		return false
	})
	total := len(out)
	per := q.PerPage
	if per <= 0 {
		per = 30
	}
	page := max(q.Page, 1)
	start := min((page-1)*per, len(out))
	out = out[start:min(start+per, len(out))]
	if !q.Count {
		total = -1
	}
	return &Page{Items: out, Total: total}, nil
}

func matches(r Record, w Record) bool {
	if w == nil {
		return true
	}
	if all, ok := w["and"].([]any); ok {
		for _, c := range all {
			if !matches(r, c.(map[string]any)) {
				return false
			}
		}
		return true
	}
	if any_, ok := w["or"].([]any); ok {
		for _, c := range any_ {
			if matches(r, c.(map[string]any)) {
				return true
			}
		}
		return len(any_) == 0
	}
	name, _ := w["field"].(string)
	if name == "" {
		return true
	}
	have, want := r[name], w["value"]
	text := func(v any) string {
		if s, ok := v.(string); ok {
			return s
		}
		b, _ := json.Marshal(v)
		return string(b)
	}
	switch w["op"] {
	case "is":
		return text(have) == text(want) || have == nil && (want == false || want == "")
	case "isNot":
		return text(have) != text(want)
	case "contains":
		return strings.Contains(strings.ToLower(text(have)), strings.ToLower(text(want)))
	case "lt":
		return text(have) < text(want)
	case "gte":
		return text(have) >= text(want)
	}
	panic(fmt.Sprintf("memory: no operator %v", w["op"]))
}

func clone(r Record) Record {
	b, _ := json.Marshal(r)
	var out Record
	_ = json.Unmarshal(b, &out)
	return out
}
