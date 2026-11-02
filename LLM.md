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

- Identity: only a signed-in respondent (their token, a credential the host saw
  in a header) is verified. A typed address is a CLAIM: under `settings.identity:
  claimed` (default) it is filed marked unverified, on the person only when the
  CRM already holds them and without changing them, joins no list, gets no welcome,
  and a list's consent is asked by the host's double opt-in (Deps.Confirm); under
  `verified` it is just an answer. Events carry `person_id` for verified
  respondents only, never an address; the filing drops the answer that named it.
- Every submission is its own response under its token, subscribe forms too, so
  a squatter's claim never hides a later real submission. A completed response
  is final; `completed:false` saves part way under the token.
- The public POST is guarded before decoding: application/json only (415), the
  address's rate (429; the host counts /64 and /48, an empty address is one shared
  budget), and the body bound (413). A body past `BodyCeiling` (1 MiB) is never
  decoded, whatever path it took.
- Sinks run after the response is stored. The CRM filing is recorded on the
  response (`filed`, `filing`), retried on the delivery schedule, listed with
  `?filed=false` and filed now by POST `.../responses/{response}/file`. Webhook
  deliveries run four at a time per org with at most 200 waiting per org (2000 in
  all); past that a delivery is recorded failed and `retry` sends it. Retries live
  in-process; a restart leaves `retrying` with `next` in the past.
- Webhook posts dial public addresses only (checked on the dialled IP), follow no
  redirect, read 4 KiB, and say only what kind of failure it was. Signature:
  `Hanzo-Signature: t=<unix>,sha256=base64(HMAC(secret, "<unix>." + body))`; a
  receiver refuses one older than `Replay` (5 minutes) or an event_id it has seen.
- Retention (days) deletes a form's old responses lazily, when the form is
  answered or its responses are read, counted from when each was stored: an
  import keeps its original time and is not taken for it.
- Numbers are bounded at 1e15; text loses its format characters (bidi, zero
  width); an address is ASCII. The platform's default sinks name no address.
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

## @hanzo/form (`packages/form`)

The public half as an npm package (ESM + types, React and react-dom as peers),
its own pnpm root. One entry, `src/index.ts`:

- `load(publicId, {endpoint?})`, `submit(publicId, submission, {endpoint?, token?})`
  (`client.ts`): `token` is an IAM bearer sent as Authorization, never in the
  body. A non-2xx throws `Problem` (`status`, and the problem document's
  `detail` as the message).
- `check(field, value)` (`check.ts`): `answer.go`'s `checkAnswer` in the browser,
  the same shapes and bounds (it cannot know the platform's per-type maximum, so
  an unbounded text field is judged by the service alone). `submission()` sends
  only the hidden names the form declares; `mint()` is the idempotency token.
- `Form` (`form.tsx`, styles in `style.ts` under `.hzform`): `form` or
  `publicId`, plus `hidden`, `endpoint`, `token`, `visitor`, `onSubmit`,
  `hideHeaders`, `hideFooter`. One token per mount; thanks only after a 2xx;
  a refusal is the service's words with every answer kept. Enter moves on, a
  digit picks, Escape is the browser's. Theme colours become `--hzform-*`.
- `createWidget(publicId, {container, opacity, ...})` (`widget.ts`): `opacity`
  is `--hzform-opacity` on the container, read by the background layer alone.

`pnpm typecheck && pnpm build && pnpm test` (vitest: validation, the submission
shape, the client) and `pnpm e2e` (Playwright: the embed against routed
api.hanzo.ai — drawing, 375/360 widths, keyboard, thanks after a 201, a double
click, a refusal). Screenshots land in `test-results/`.

## Develop

`go test ./...` (the in-memory Store in memory_test.go reads the where clauses
this package writes). After changing an op's doc comment: `go generate -run zipdoc ./...`.
The importer: `cd cmd/form && go test ./...`.
