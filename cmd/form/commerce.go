package main

// commerce.go — commerce's forms, subscribers and submissions, read through
// commerce's own datastore and models at the version go.mod pins. Nothing is
// written to the store.

import (
	"context"
	"fmt"
	"os"

	"github.com/hanzoai/commerce/datastore"
	"github.com/hanzoai/commerce/db"
	"github.com/hanzoai/commerce/log"
	"github.com/hanzoai/commerce/models/form"
	"github.com/hanzoai/commerce/models/submission"
	"github.com/hanzoai/commerce/models/subscriber"
	"github.com/hanzoai/commerce/util/nscontext"
	"github.com/hanzoai/namespace"
)

// commerce is one org's records in one commerce store.
type commerce struct {
	m  *db.Manager
	ds *datastore.Datastore
}

// openCommerce opens the store named store under commerce's data directory and
// reads org's records in it. A store that is not there is refused, so commerce's
// open never makes an empty one. A keyed store needs COMMERCE_KMS_MASTER_KEY and
// a build linked against libsqlcipher, as commerce does.
func openCommerce(ctx context.Context, dir, store, org string) (*commerce, error) {
	ns, err := namespace.OrgProject(store, "")
	if err != nil {
		return nil, err
	}
	path, err := namespace.Path(dir, ns, "commerce")
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("no commerce store: %w", err)
	}
	log.SetVerbose(false)
	cfg := db.DefaultConfig()
	cfg.DataDir = dir
	cfg.EnableVectorSearch = false
	m, err := db.NewManager(cfg)
	if err != nil {
		return nil, err
	}
	t, err := m.Org(store)
	if err != nil {
		_ = m.Close()
		return nil, err
	}
	return &commerce{m: m, ds: datastore.NewWithDB(nscontext.WithNamespace(ctx, org), t)}, nil
}

func (c *commerce) Close() error { return c.m.Close() }

// Forms reads every form, in the store's order.
func (c *commerce) Forms(fn func(Origin) error) error {
	return each(form.Query(c.ds), func() *form.Form { return form.New(c.ds) }, func(f *form.Form) error {
		o := Origin{ID: f.Id(), Name: f.Name, Type: string(f.Type), Thanks: string(f.ThankYou.Type), URL: f.ThankYou.Url, Welcome: f.SendWelcome}
		if f.Forward.Enabled {
			o.Forward, o.Recipient = f.Forward.Email, f.Forward.Name
		}
		return fn(o)
	})
}

// Count answers how many records of kind the form holds; every record of the
// kind when id is empty.
func (c *commerce) Count(id, kind string) (int, error) {
	q, err := c.query(id, kind)
	if err != nil {
		return 0, err
	}
	return q.Count()
}

// Rows reads every record of kind the form holds, in the store's order.
func (c *commerce) Rows(id, kind string, fn func(Row) error) error {
	q, err := c.query(id, kind)
	if err != nil {
		return err
	}
	if kind == Subscriber {
		return each(q, func() *subscriber.Subscriber { return subscriber.New(c.ds) }, func(s *subscriber.Subscriber) error {
			return fn(Row{ID: s.Id(), At: s.CreatedAt, Email: s.Email, Metadata: s.Metadata})
		})
	}
	return each(q, func() *submission.Submission { return submission.New(c.ds) }, func(s *submission.Submission) error {
		return fn(Row{ID: s.Id(), At: s.CreatedAt, Email: s.Email, Metadata: s.Metadata})
	})
}

// query is the records of kind a form holds: a subscriber names its form, a
// submission is the form's child.
func (c *commerce) query(id, kind string) (datastore.Query, error) {
	if kind == Subscriber {
		q := subscriber.Query(c.ds)
		if id != "" {
			q = q.Filter("FormId=", id)
		}
		return q, nil
	}
	q := submission.Query(c.ds)
	if id != "" {
		f := form.New(c.ds)
		if err := f.SetKey(id); err != nil {
			return nil, err
		}
		q = q.Ancestor(f.Key())
	}
	return q, nil
}

// each reads, one at a time, as many entities as q counts. One it counted and
// cannot read is an error, never the end.
func each[T any](q datastore.Query, blank func() T, fn func(T) error) error {
	n, err := q.Count()
	if err != nil {
		return err
	}
	it := q.Run()
	if c, ok := it.(interface{ Close() error }); ok {
		defer func() { _ = c.Close() }()
	}
	for i := range n {
		v := blank()
		if _, err := it.Next(v); err != nil {
			return fmt.Errorf("reading %d of %d: %w", i+1, n, err)
		}
		if err := fn(v); err != nil {
			return err
		}
	}
	return nil
}
