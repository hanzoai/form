package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/hanzoai/commerce/datastore"
	"github.com/hanzoai/commerce/db"
	"github.com/hanzoai/commerce/models/form"
	"github.com/hanzoai/commerce/models/submission"
	"github.com/hanzoai/commerce/models/subscriber"
	"github.com/hanzoai/commerce/models/types/thankyou"
	"github.com/hanzoai/commerce/util/nscontext"
)

// store writes a commerce store with commerce's own models, the way its form
// routes did: a subscribe form with two subscribers, a submit form with one
// submission, and a subscriber that names no form.
func store(t *testing.T) (dir string, news, contact string) {
	t.Helper()
	dir = t.TempDir()
	cfg := db.DefaultConfig()
	cfg.DataDir = dir
	cfg.EnableVectorSearch = false
	m, err := db.NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	org, err := m.Org("acme")
	if err != nil {
		t.Fatal(err)
	}
	ds := datastore.NewWithDB(nscontext.WithNamespace(context.Background(), "acme"), org)
	create := func(e interface{ Create() error }) {
		if err := e.Create(); err != nil {
			t.Fatal(err)
		}
	}
	f := form.New(ds)
	f.Name, f.Type, f.SendWelcome = "Newsletter", "subscribe", true
	f.Forward.Enabled, f.Forward.Email, f.Forward.Name = true, "ops@acme.com", "Ops"
	f.ThankYou.Type, f.ThankYou.Url = thankyou.Redirect, "https://acme.com/thanks"
	create(f)
	c := form.New(ds)
	c.Name, c.Type = "Contact", form.Submit
	create(c)
	for _, e := range []string{"Ada@Example.com", "bob@example.com"} {
		s := subscriber.New(ds)
		s.Email, s.FormId, s.Parent = e, f.Id(), f.Key()
		s.Metadata = map[string]any{"firstName": "Ada"}
		create(s)
	}
	stray := subscriber.New(ds)
	stray.Email = "stray@example.com"
	create(stray)
	sub := submission.New(ds)
	sub.Email, sub.Parent = "cy@example.com", c.Key()
	sub.Metadata = map[string]any{"message": "hello", "count": 2}
	create(sub)
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	return dir, f.Id(), c.Id()
}

func TestCommerceReadsWhatCommerceWrote(t *testing.T) {
	dir, news, contact := store(t)
	c, err := openCommerce(context.Background(), dir, "acme", "acme")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	var forms []Origin
	if err := c.Forms(func(o Origin) error { forms = append(forms, o); return nil }); err != nil {
		t.Fatal(err)
	}
	want := []Origin{
		{ID: news, Name: "Newsletter", Type: "subscribe", Forward: "ops@acme.com", Recipient: "Ops", Thanks: "redirect", URL: "https://acme.com/thanks", Welcome: true},
		{ID: contact, Name: "Contact", Type: "submit", Thanks: "disabled"},
	}
	if len(forms) != 2 || forms[0] != want[0] || forms[1] != want[1] {
		t.Fatalf("forms %+v, want %+v", forms, want)
	}
	rows := func(id, kind string) []Row {
		var out []Row
		if err := c.Rows(id, kind, func(r Row) error { out = append(out, r); return nil }); err != nil {
			t.Fatal(err)
		}
		return out
	}
	subs := rows(news, Subscriber)
	if len(subs) != 2 || subs[0].Email != "ada@example.com" || subs[1].Email != "bob@example.com" || subs[0].Metadata["firstName"] != "Ada" || subs[0].At.IsZero() {
		t.Fatalf("subscribers %+v", subs)
	}
	sent := rows(contact, Submission)
	if len(sent) != 1 || sent[0].Email != "cy@example.com" || sent[0].Metadata["message"] != "hello" {
		t.Fatalf("submissions %+v", sent)
	}
	if got := rows(news, Submission); len(got) != 0 {
		t.Fatalf("a subscribe form's submissions %+v", got)
	}
	if n, err := c.Count("", Subscriber); err != nil || n != 3 {
		t.Fatalf("every subscriber: %d %v", n, err)
	}
}

func TestDryRunCounts(t *testing.T) {
	dir, news, contact := store(t)
	c, err := openCommerce(context.Background(), dir, "acme", "acme")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	var out bytes.Buffer
	if err := count(c, &out); err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		"commerce-" + strings.ToLower(news) + "\tsubscribe\t\"Newsletter\"\tsubscriber 2\tsubmission 0",
		"commerce-" + strings.ToLower(contact) + "\tform\t\"Contact\"\tsubscriber 0\tsubmission 1",
		"total: forms 2, subscriber 2 (1 name no form), submission 1 (0 name no form)",
	} {
		if !strings.Contains(out.String(), line) {
			t.Errorf("dry run lacks %q:\n%s", line, out.String())
		}
	}
}

func TestAMissingStoreIsNotMade(t *testing.T) {
	dir := t.TempDir()
	if _, err := openCommerce(context.Background(), dir, "nobody", "nobody"); err == nil || !strings.Contains(err.Error(), "no commerce store") {
		t.Fatalf("open: %v", err)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Fatalf("the open wrote %v", left)
	}
}
