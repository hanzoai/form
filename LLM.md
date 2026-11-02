# hanzoai/form

Forms: a form is defined once (fields, screens, hidden values, theme, sinks),
published at a public address, answered anonymously or signed in, and every
completed response is routed where the form says.

Served at `/v1/form` on api.hanzo.ai by hanzo-inc/cloud, which composes `form.New`
as it composes hanzoai/iam's `server.NewApp`: `app.Use(form.New(deps))`.

## Shape

One package, `form`. `New(Deps) *zip.App` registers every route as a typed op
(REST, OpenAPI, MCP tool, CLI, SDK from one registration). The host supplies
everything that is a host's fact (`Deps`):

| Dep | What | cloud's answer |
|---|---|---|
| `Store` | one Base per org: Install, Get, List (Base where clause), Put, Delete | the base plane (base_import/upsert/get/list/delete) |
| `Platform` | the org whose Base holds the public index | `admin` |
| `Caller` | org acted in, org admin, signed-in subject + email | principal + identity boundary |
| `Client` | caller IP, GPC/DNT | cloud.ClientIP, cloud.OptedOut |
| `Intended` | anti-forgery verdict on a change | cloud.CSRF |
| `Allow` | deployment-wide rate window | cloud.Window |
| `Settings` | platform config (types, limits, default sinks, retention) | settings fleet config, product `form`, edited by `/v1/admin/form/settings` |
| `Person` | CRM person id of an address (the join key) | apps/crm PersonID |
| `Emit` | form_viewed / form_started / form_submitted | event plane (EventCapture) |
| `File` | CRM filing | apps/crm FileForm |
| `Mail` | forward + welcome | notify rail, bounded |
| `Secrets` | webhook signing secrets | KMS `orgs/<org>/form/WEBHOOK_<ID>@prod` |

## Routes (all `/v1/form`)

Org admin: `forms` (POST/GET), `forms/{id}` (GET/PUT/PATCH/DELETE; id or slug;
PUT on an unused slug creates), `forms/{id}/responses[/{response}]` (GET, DELETE),
`forms/{id}/summary`, `forms/{id}/export` (json|csv, paged), `forms/{id}/import`
(batch 500, keyed by external id; subscribe forms by address),
`forms/{id}/webhooks[/{webhook}]` (CRUD), `.../deliveries` (GET),
`.../deliveries/{delivery}/retry` (POST), `themes[/{id}]` (CRUD).

Public (no credential): `GET public/{id}`, `POST public/{id}/responses`.
A public id is `<org>.<slug or id>`, resolved through the index in the platform
org's Base, so an unknown org is a 404 that opens nothing.

## Records (org Base)

`forms`, `form_themes`, `form_responses` (relation to forms, cascade),
`form_webhooks` (cascade), `form_deliveries` (cascade from webhooks); platform
org: `form_index`. Response id = derived(form, token) — a double click is one
response; subscribe = derived(form, address); import = derived(form, external).

## Rules worth knowing

- Identity: token (signed-in person) > the email field (CRM mapping's, else the
  first email field) > the visitor id. Events and filings carry `person_id`
  (Deps.Person), never the address; the filing drops the answer that named it.
- A response completed is final; `completed:false` saves part way under the token.
- Sinks run after the response is stored; a failing sink is logged, never the
  response. Webhook retries (0, 30s, 2m, 10m, 1h) live in-process; a restart
  leaves `retrying` with `next` in the past, and `retry` sends it.
- Webhook posts dial public addresses only (checked on the dialled IP), follow no
  redirect, read 4 KiB. Signature: `Hanzo-Signature: sha256=base64(HMAC(secret, body))`.
- Retention (days) deletes a form's old responses lazily, when the form is
  answered or its responses are read.
- Kinds: `form`, `subscribe`. `survey` is the next kind (audience, window,
  identified/anonymous, scoring) and adds fields, never changes one.
- Logic jumps are not served.

## Import from commerce (`cmd/form`)

`form import` brings hanzoai/commerce's forms, subscribers and submissions in.
It is its own module: no API serves commerce's forms in production (cloud mounts
commerce's resources, not its form routes), so it reads commerce's store through
commerce's own `datastore` and models, pinned at v1.50.187, the last commerce
that has them, and that tree stays out of this module.

- Read a copy of commerce's data dir (cloud's is `$CLOUD_DATA_DIR/commerce`):
  `-data DIR -org NAMESPACE`; `-store system` reads the shared store commerce's
  form routes wrote. A keyed store needs `COMMERCE_KMS_MASTER_KEY` (hex) and a
  libsqlcipher build: `CGO_ENABLED=1 CGO_CFLAGS="-DSQLITE_HAS_CODEC -DSQLITE_USE_URI=1"
  CGO_LDFLAGS=-lsqlcipher go build -tags "libsqlite3 sqlite_fts5"`.
- `-dry` counts per form, and the records that name no form. Then `TOKEN=<IAM
  bearer of the target org's admin> form import ...`: each form is a PUT on
  `commerce-<id>`, its records go 500 a batch to `import`, a cursor (`-cursor`)
  is written after each acknowledged batch and removed when the run ends.
- Mapping: Name → title (and `crm.list` on subscribe); type subscribe →
  subscribe, else form; an email field (required on subscribe) plus a
  short_text field per metadata key; https ThankYou redirect → `thanks.redirect`;
  enabled Forward → `sinks.forward`; events off. A welcome is not carried: commerce
  holds a provider template id, never a text body. A record is
  `Imported{external "<kind>_<id>", at CreatedAt, email, answers = metadata, source commerce}`.
  Subscribers are a form's by `FormId`, submissions by commerce's parent key.

## Develop

`go test ./...` (the in-memory Store in memory_test.go reads the where clauses
this package writes). After changing an op's doc comment: `go generate -run zipdoc ./...`.
The importer: `cd cmd/form && go test ./...`.
