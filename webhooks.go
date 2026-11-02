package form

// webhooks.go — the addresses a form's completed responses are posted to, and
// the record of every post.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"

	"github.com/zap-proto/zip"
)

// WebhookIn makes or replaces a webhook of a form.
type WebhookIn struct {
	// ID is the form's id or slug, from the path.
	ID string `json:"id"`
	// Webhook is the webhook's id, from the path of a replace.
	Webhook string `json:"webhook"`
	// Tag names the webhook in its form, at most 64 characters.
	Tag string `json:"tag"`
	// URL is the https address posted to. Required.
	URL string `json:"url"`
	// Enabled is whether responses are posted. Default true.
	Enabled *bool `json:"enabled"`
	// Secret sets the signing secret: 16 to 256 characters. Absent on a make,
	// one is minted; absent on a replace, the secret held is kept.
	Secret string `json:"secret" url:"-"`
	// Rotate mints a new secret on a replace, and answers it.
	Rotate bool `json:"rotate"`
}

// WebhookRef names one webhook of one form.
type WebhookRef struct {
	// ID is the form's id or slug, from the path.
	ID string `json:"id"`
	// Webhook is the webhook's id, from the path.
	Webhook string `json:"webhook"`
}

// WebhookList is a form's webhooks.
type WebhookList struct {
	Items []Webhook `json:"items"`
}

// DeliveriesIn asks for a page of one webhook's deliveries.
type DeliveriesIn struct {
	// ID is the form's id or slug, from the path.
	ID string `json:"id"`
	// Webhook is the webhook's id, from the path.
	Webhook string `json:"webhook"`
	// Page is the page, from 1; PageSize at most 200, default 50.
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
}

// DeliveryList is one page of a webhook's deliveries, newest first.
type DeliveryList struct {
	Items    []Delivery `json:"items"`
	Page     int        `json:"page"`
	PageSize int        `json:"page_size"`
	Total    int        `json:"total"`
}

// DeliveryRef names one delivery of one webhook.
type DeliveryRef struct {
	// ID is the form's id or slug, from the path.
	ID string `json:"id"`
	// Webhook is the webhook's id, from the path.
	Webhook string `json:"webhook"`
	// Delivery is the delivery's id, from the path.
	Delivery string `json:"delivery"`
}

// secretName is the name a webhook's secret is kept under in the org's KMS.
func secretName(id string) string { return "WEBHOOK_" + strings.ToUpper(id) }

// mint is a new signing secret: 32 random bytes, base64url.
func mint() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return "whsec_" + base64.RawURLEncoding.EncodeToString(b[:])
}

// webhookOf is a stored webhook as the wire carries it.
func webhookOf(r Record) Webhook {
	w := Webhook{ID: field(r, "id"), Form: field(r, "form"), Tag: field(r, "tag"), URL: field(r, "url"),
		Created: wire(r, "created"), Updated: wire(r, "updated")}
	w.Enabled, _ = r["enabled"].(bool)
	return w
}

// webhookAt reads a webhook of form.
func (s *service) webhookAt(ctx context.Context, org, form, id string) (*Webhook, error) {
	if !recordID(strings.TrimSpace(id)) {
		return nil, zip.ErrNotFound("form: no such webhook")
	}
	r, err := s.d.Store.Get(ctx, org, Webhooks, strings.TrimSpace(id))
	if errors.Is(err, ErrNotFound) || err == nil && field(r, "form") != form {
		return nil, zip.ErrNotFound("form: no such webhook")
	}
	if err != nil {
		return nil, s.failed("the webhook could not be read", org, err)
	}
	w := webhookOf(r)
	return &w, nil
}

// checkWebhook reads the parts of a webhook a write names.
func checkWebhook(in *WebhookIn) (tag, url string, problems []string) {
	tag, ok := line(in.Tag, 64)
	if !ok {
		problems = append(problems, "tag is one line of at most 64 characters")
	}
	url, ok = https(in.URL)
	if !ok {
		problems = append(problems, "url is an https address")
	}
	if in.Secret != "" && (len(in.Secret) < 16 || len(in.Secret) > 256 || strings.ContainsFunc(in.Secret, func(r rune) bool { return r <= ' ' || r == 0x7f })) {
		problems = append(problems, "secret is 16 to 256 printable characters")
	}
	return tag, url, problems
}

// keep seals a webhook's secret in the org's KMS.
func (s *service) keep(ctx context.Context, org, id, secret string) error {
	if s.d.Secrets == nil {
		return zip.Errorf(503, "form: this deployment keeps no secrets, so a webhook cannot be signed")
	}
	if err := s.d.Secrets.Put(ctx, org, secretName(id), []byte(secret)); err != nil {
		return s.failed("the webhook's secret could not be kept", org, err)
	}
	return nil
}

// forget drops a webhook's secret.
func (s *service) forget(ctx context.Context, org, id string) error {
	if s.d.Secrets == nil {
		return nil
	}
	if err := s.d.Secrets.Delete(ctx, org, secretName(id)); err != nil && !errors.Is(err, ErrNoSecret) {
		return s.failed("the webhook's secret could not be dropped", org, err)
	}
	return nil
}

// Adds a webhook to a form: every completed response is posted to its https
// address as JSON, signed with Hanzo-Signature: t=<unix seconds>,sha256=<base64
// of HMAC-SHA256(secret, "<seconds>." + body)>; a receiver refuses a timestamp
// older than five minutes and an event_id it has seen. The secret is the one
// given (16 to 256 characters) or one minted, kept in KMS and answered here
// once. A form has at
// most the platform's number of webhooks. An org admin only.
//
// Example: {"tag":"sales","url":"https://hooks.example.com/forms"}
func (s *service) createWebhook(ctx context.Context, in *WebhookIn) (*Webhook, error) {
	org, err := s.change(ctx)
	if err != nil {
		return nil, err
	}
	f, err := s.lookup(ctx, org, in.ID)
	if err != nil {
		return nil, err
	}
	tag, url, problems := checkWebhook(in)
	if len(problems) > 0 {
		return nil, zip.ErrBadRequest("form: " + strings.Join(problems, "; "))
	}
	have, err := s.d.Store.List(ctx, org, Webhooks, Query{Where: leaf("form", "is", f.ID), PerPage: 1, Count: true})
	if err != nil {
		return nil, s.failed("the form's webhooks could not be read", org, err)
	}
	if most := s.settings(ctx).Limits.Webhooks; have.Total >= most {
		return nil, zip.ErrBadRequest("form: a form has at most " + strconv.Itoa(most) + " webhooks")
	}
	id := fresh()
	secret := in.Secret
	if secret == "" {
		secret = mint()
	}
	if err := s.keep(ctx, org, id, secret); err != nil {
		return nil, err
	}
	enabled := in.Enabled == nil || *in.Enabled
	if err := s.d.Store.Put(ctx, org, Webhooks, Record{"id": id, "form": f.ID, "tag": tag, "url": url, "enabled": enabled}); err != nil {
		_ = s.forget(ctx, org, id)
		return nil, s.failed("the webhook could not be saved", org, err)
	}
	w, err := s.webhookAt(ctx, org, f.ID, id)
	if err != nil {
		return nil, err
	}
	w.Secret = secret
	return w, nil
}

// Lists a form's webhooks. Secrets are never read back. An org admin only.
func (s *service) listWebhooks(ctx context.Context, in *FormRef) (*WebhookList, error) {
	org, f, err := s.adminForm(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	got, err := s.d.Store.List(ctx, org, Webhooks, Query{Where: leaf("form", "is", f.ID), Sort: "created", PerPage: 100})
	if err != nil {
		return nil, s.failed("the form's webhooks could not be read", org, err)
	}
	out := &WebhookList{Items: []Webhook{}}
	for _, r := range got.Items {
		out.Items = append(out.Items, webhookOf(r))
	}
	return out, nil
}

// Reads one webhook of a form. Its secret is never read back. An org admin only.
func (s *service) getWebhook(ctx context.Context, in *WebhookRef) (*Webhook, error) {
	org, f, err := s.adminForm(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	return s.webhookAt(ctx, org, f.ID, in.Webhook)
}

// Replaces a webhook's tag, address and whether it is enabled. A secret given is
// kept in place of the one held, rotate mints a new one, and either is answered
// once; otherwise the secret held stays. An org admin only.
func (s *service) replaceWebhook(ctx context.Context, in *WebhookIn) (*Webhook, error) {
	org, err := s.change(ctx)
	if err != nil {
		return nil, err
	}
	f, err := s.lookup(ctx, org, in.ID)
	if err != nil {
		return nil, err
	}
	w, err := s.webhookAt(ctx, org, f.ID, in.Webhook)
	if err != nil {
		return nil, err
	}
	tag, url, problems := checkWebhook(in)
	if len(problems) > 0 {
		return nil, zip.ErrBadRequest("form: " + strings.Join(problems, "; "))
	}
	secret := in.Secret
	if in.Rotate && secret == "" {
		secret = mint()
	}
	if secret != "" {
		if err := s.keep(ctx, org, w.ID, secret); err != nil {
			return nil, err
		}
	}
	enabled := in.Enabled == nil || *in.Enabled
	if err := s.d.Store.Put(ctx, org, Webhooks, Record{"id": w.ID, "tag": tag, "url": url, "enabled": enabled}); err != nil {
		return nil, s.failed("the webhook could not be saved", org, err)
	}
	out, err := s.webhookAt(ctx, org, f.ID, w.ID)
	if err != nil {
		return nil, err
	}
	out.Secret = secret
	return out, nil
}

// Deletes a webhook, its deliveries and its secret. An org admin only.
func (s *service) deleteWebhook(ctx context.Context, in *WebhookRef) (*struct{}, error) {
	org, err := s.change(ctx)
	if err != nil {
		return nil, err
	}
	f, err := s.lookup(ctx, org, in.ID)
	if err != nil {
		return nil, err
	}
	w, err := s.webhookAt(ctx, org, f.ID, in.Webhook)
	if err != nil {
		return nil, err
	}
	if err := s.forget(ctx, org, w.ID); err != nil {
		return nil, err
	}
	if err := s.d.Store.Delete(ctx, org, Webhooks, w.ID); err != nil {
		return nil, s.failed("the webhook could not be deleted", org, err)
	}
	return &struct{}{}, nil
}

// Lists a webhook's deliveries, newest first: each response posted, how many
// attempts it took, the last status the address answered and why it failed. An
// org admin only.
func (s *service) listDeliveries(ctx context.Context, in *DeliveriesIn) (*DeliveryList, error) {
	org, f, err := s.adminForm(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	w, err := s.webhookAt(ctx, org, f.ID, in.Webhook)
	if err != nil {
		return nil, err
	}
	page, size := paged(in.Page, in.PageSize, 50, 200)
	got, err := s.d.Store.List(ctx, org, Deliveries, Query{Where: leaf("webhook", "is", w.ID), Sort: "-created", Page: page, PerPage: size, Count: true})
	if err != nil {
		return nil, s.failed("the webhook's deliveries could not be read", org, err)
	}
	out := &DeliveryList{Items: []Delivery{}, Page: page, PageSize: size, Total: got.Total}
	for _, r := range got.Items {
		out.Items = append(out.Items, deliveryOf(r))
	}
	return out, nil
}

// Posts a delivery's response to its webhook again, now, and answers how it
// went. A delivery that failed, or was waiting on a retry when the service
// restarted, is sent this way. An org admin only.
func (s *service) redeliver(ctx context.Context, in *DeliveryRef) (*Delivery, error) {
	org, err := s.change(ctx)
	if err != nil {
		return nil, err
	}
	f, err := s.lookup(ctx, org, in.ID)
	if err != nil {
		return nil, err
	}
	w, err := s.webhookAt(ctx, org, f.ID, in.Webhook)
	if err != nil {
		return nil, err
	}
	if !recordID(strings.TrimSpace(in.Delivery)) {
		return nil, zip.ErrNotFound("form: no such delivery")
	}
	d, err := s.d.Store.Get(ctx, org, Deliveries, strings.TrimSpace(in.Delivery))
	if errors.Is(err, ErrNotFound) || err == nil && field(d, "webhook") != w.ID {
		return nil, zip.ErrNotFound("form: no such delivery")
	}
	if err != nil {
		return nil, s.failed("the delivery could not be read", org, err)
	}
	rec, err := s.responseAt(ctx, org, f.ID, field(d, "response"))
	if err != nil {
		return nil, err
	}
	got := s.out.attempt(ctx, org, *f, responseOf(rec), *w, deliveryOf(d), false)
	return &got, nil
}
