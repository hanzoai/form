// Package form is forms: a form is defined once (its fields, screens, hidden
// values, theme and sinks), answered at a public address by anyone or by a
// signed-in person, and every completed response is routed where the form says:
// the event stream, the CRM, a forwarding address, a welcome mail and the form's
// webhooks.
//
// It is a zip App ([New]) a host composes, the way hanzo-inc/cloud composes
// hanzoai/iam: every route is a typed op, so one registration is the REST
// route, the OpenAPI operation, the MCP tool, the CLI command and the SDK method.
//
//	/v1/form/forms[/{id}]                       the org's forms (org admin)
//	/v1/form/forms/{id}/responses[/{response}]   its responses
//	/v1/form/forms/{id}/{summary,export,import}  what they add up to, out and in
//	/v1/form/forms/{id}/webhooks[/{webhook}]     where they are posted, and how it went
//	/v1/form/themes[/{id}]                      the org's themes
//	/v1/form/public/{id}[/responses]            the form as drawn, and answering it (no credential)
//
// THE HOST DECIDES WHO, AND WHERE THINGS ARE KEPT. Every record lives in the
// org's own store ([Store]: Hanzo Base, one per org); who is calling, which org
// they act in and whether they administer it are the host's identity boundary
// ([Deps.Caller]), never a field of a request. A public submission names its org
// in the form's public id and nothing else.
package form

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/zap-proto/zip"
)

//go:generate go run github.com/zap-proto/zip/cmd/zipdoc

// Caller is who an org-scoped request is, as the host's identity boundary
// attested it.
type Caller struct {
	// Org is the org the caller acts in; empty for nobody.
	Org string
	// Admin is whether they administer Org (or hold platform authority).
	Admin bool
	// Subject is the signed-in person's IAM subject, set only for a person
	// holding their own token.
	Subject string
	// Email is that person's address, from their token.
	Email string
}

// Client is what the host knows of the machine a request came from.
type Client struct {
	// IP is the caller's address, as the host reads one; empty inside the cluster.
	IP string
	// OptedOut is a browser that sent Sec-GPC or DNT: nothing about it is stated
	// on the event stream.
	OptedOut bool
}

// Event is one occurrence the service states on the host's event stream.
type Event struct {
	// Name is form_viewed, form_started or form_submitted.
	Name string
	// Form is the form's id, Public its public id.
	Form, Public string
	// Response is the response's id; empty on form_viewed.
	Response string
	// PersonID is the CRM person the response is from, when the service knows
	// it. Never an address.
	PersonID string
	// Visitor is the page's visitor id, when it sent one.
	Visitor string
	// Fields are the refs of the fields answered, never what was said.
	Fields []string
}

// The occurrences the events sink states.
const (
	Viewed    = "form_viewed"
	Started   = "form_started"
	Submitted = "form_submitted"
)

// Filing is one completed response as the CRM files it: on the timeline of the
// person it is from, or on the org's own timeline when it names nobody.
type Filing struct {
	// Ref is the timeline entry's key, "form:<response id>": filing it twice is
	// one entry.
	Ref string
	// Form is the form's id, Public its public id, Title its title.
	Form, Public, Title string
	// Kind is the form's kind.
	Kind string
	// PersonID is the CRM person; empty files on the org's own timeline.
	PersonID string
	// Email, Name and Company fill the person: created when missing, given only
	// the fields still empty when held.
	Email, Name, Company string
	// List is the CRM list the person joins; empty joins none.
	List string
	// Answers are the answers as question and text, in field order, without the
	// one that named the person's address.
	Answers []QA
	// Hidden are the values the page passed in.
	Hidden map[string]string
	// Visitor is the page's visitor id; Subject the signed-in person's subject.
	Visitor, Subject string
	// Path is the page the form was answered on.
	Path string
	// At is when the response was completed.
	At time.Time
}

// QA is one question and what was said to it, as text.
type QA struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// Mail is one mail the service asks the host to send.
type Mail struct {
	// To is the address, Name who it is.
	To, Name string
	// Subject is the subject line, Text the body as plain text.
	Subject, Text string
	// Kind is forward (a response to the form's own address) or welcome (to the
	// respondent).
	Kind string
	// Form is the form's id: the host bounds and counts mail per form.
	Form string
}

// Secrets keeps a webhook's signing secret: in KMS, under the org, never in the
// store. A missing secret is ErrNoSecret.
type Secrets interface {
	Put(ctx context.Context, org, name string, value []byte) error
	Get(ctx context.Context, org, name string) ([]byte, error)
	Delete(ctx context.Context, org, name string) error
}

// ErrNoSecret is a secret the keeper does not hold.
var ErrNoSecret = errors.New("form: no such secret")

// Deps is what a host hands the service. Store, Caller, Client and Settings are
// required; a nil sink is a sink the host does not have, and the service says
// so by not filing there.
type Deps struct {
	// Store is where every record lives: one Base per org.
	Store Store
	// Platform is the org whose Base holds the public index: the platform's own.
	Platform string
	// Caller is who an org-scoped request is.
	Caller func(ctx context.Context) Caller
	// Client is the machine a request came from.
	Client func(ctx context.Context) Client
	// Intended is the anti-forgery verdict on a change; nil admits.
	Intended func(ctx context.Context) error
	// Allow spends one from key's budget of limit per window, across the
	// deployment, and reports whether there was one to spend.
	Allow func(ctx context.Context, key string, limit int, window time.Duration) bool
	// Settings is the platform's configuration, its defaults when none is saved.
	Settings func(ctx context.Context) Settings
	// Person is the CRM person an address is: the join key every event and
	// filing carries.
	Person func(email string) string
	// Emit states an occurrence on the event stream, detached.
	Emit func(ctx context.Context, org string, e Event)
	// File files a completed response in the org's CRM.
	File func(ctx context.Context, org string, f Filing) error
	// Mail sends one mail in the org's name.
	Mail func(ctx context.Context, org string, m Mail) error
	// Secrets keeps webhook signing secrets.
	Secrets Secrets
	// HTTP posts webhook deliveries. Nil is a client that refuses any address
	// inside a private network.
	HTTP *http.Client
	// Log is where the service says what it could not do.
	Log *slog.Logger
	// Now is the clock. Nil is time.Now.
	Now func() time.Time
}

// service carries the dependencies onto every typed op.
type service struct {
	d   Deps
	org installs
	out *deliverer
}

// New is the forms App, every route registered. A host composes it with Use.
func New(d Deps) *zip.App {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.HTTP == nil {
		d.HTTP = guarded()
	}
	if d.Person == nil {
		d.Person = func(string) string { return "" }
	}
	s := &service{d: d}
	s.out = newDeliverer(s)
	app := zip.New(zip.Config{AppName: "form", DisableStartupMessage: true})
	routes(app, s)
	if err := app.Build(); err != nil {
		panic("form: app does not compose: " + err.Error())
	}
	return app
}

// Prefix is the address every op is under.
const Prefix = "/v1/form"

// routes registers every op. Static segments come before the parameters beside
// them, so a first-match router resolves each address one way.
func routes(app *zip.App, s *service) {
	g := app.Group(Prefix)

	g.Get("/public/:id", s.publicForm, zip.WithOperationID("get_public_form"))
	g.Post("/public/:id/responses", s.submit, zip.WithOperationID("submit_response"), zip.WithStatus(http.StatusCreated))

	g.Get("/themes", s.listThemes, zip.WithOperationID("list_themes"))
	g.Post("/themes", s.createTheme, zip.WithOperationID("create_theme"), zip.WithStatus(http.StatusCreated))
	g.Get("/themes/:id", s.getTheme, zip.WithOperationID("get_theme"))
	g.Put("/themes/:id", s.replaceTheme, zip.WithOperationID("replace_theme"))
	g.Delete("/themes/:id", s.deleteTheme, zip.WithOperationID("delete_theme"), zip.WithStatus(http.StatusNoContent))

	g.Get("/forms", s.listForms, zip.WithOperationID("list_forms"))
	g.Post("/forms", s.createForm, zip.WithOperationID("create_form"), zip.WithStatus(http.StatusCreated))
	g.Get("/forms/:id", s.getForm, zip.WithOperationID("get_form"))
	g.Put("/forms/:id", s.replaceForm, zip.WithOperationID("replace_form"))
	g.Patch("/forms/:id", s.updateForm, zip.WithOperationID("update_form"))
	g.Delete("/forms/:id", s.deleteForm, zip.WithOperationID("delete_form"), zip.WithStatus(http.StatusNoContent))

	g.Get("/forms/:id/summary", s.summary, zip.WithOperationID("summarize_form"))
	g.Get("/forms/:id/export", s.export, zip.WithOperationID("export_responses"))
	g.Post("/forms/:id/import", s.importResponses, zip.WithOperationID("import_responses"))
	g.Get("/forms/:id/responses", s.listResponses, zip.WithOperationID("list_responses"))
	g.Get("/forms/:id/responses/:response", s.getResponse, zip.WithOperationID("get_response"))
	g.Delete("/forms/:id/responses/:response", s.deleteResponse, zip.WithOperationID("delete_response"), zip.WithStatus(http.StatusNoContent))

	g.Get("/forms/:id/webhooks", s.listWebhooks, zip.WithOperationID("list_webhooks"))
	g.Post("/forms/:id/webhooks", s.createWebhook, zip.WithOperationID("create_webhook"), zip.WithStatus(http.StatusCreated))
	g.Get("/forms/:id/webhooks/:webhook", s.getWebhook, zip.WithOperationID("get_webhook"))
	g.Put("/forms/:id/webhooks/:webhook", s.replaceWebhook, zip.WithOperationID("replace_webhook"))
	g.Delete("/forms/:id/webhooks/:webhook", s.deleteWebhook, zip.WithOperationID("delete_webhook"), zip.WithStatus(http.StatusNoContent))
	g.Get("/forms/:id/webhooks/:webhook/deliveries", s.listDeliveries, zip.WithOperationID("list_deliveries"))
	g.Post("/forms/:id/webhooks/:webhook/deliveries/:delivery/retry", s.redeliver, zip.WithOperationID("redeliver"))
}

// admin admits an administrator of the org the caller acts in, and answers it.
func (s *service) admin(ctx context.Context) (string, error) {
	c := s.d.Caller(ctx)
	if c.Org == "" {
		return "", zip.ErrUnauthorized("form: sign in to an org")
	}
	if !c.Admin {
		return "", zip.ErrForbidden("form: an org admin manages its forms and reads their responses")
	}
	return c.Org, nil
}

// change admits an administrator's change: admin, then the anti-forgery verdict.
func (s *service) change(ctx context.Context) (string, error) {
	org, err := s.admin(ctx)
	if err != nil {
		return "", err
	}
	if s.d.Intended != nil {
		if err := s.d.Intended(ctx); err != nil {
			return "", err
		}
	}
	return org, s.ready(ctx, org)
}

// stamp is a time as the wire carries it.
func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// failed is the answer to a store that did not answer: logged here, a 503 there.
func (s *service) failed(what string, org string, err error) error {
	var he *zip.HTTPError
	if errors.As(err, &he) && he.Status < 500 {
		return err
	}
	s.d.Log.Warn("form: "+what, "org", org, "err", err)
	return zip.Errorf(http.StatusServiceUnavailable, "form: %s", what)
}

// raw is v as a JSON value, for a record's json field.
func raw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
