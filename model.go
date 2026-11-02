package form

// model.go — the values the service speaks: a form, its fields and screens, a
// theme, a response and its answers, a webhook and its deliveries. Every type
// here is a wire type of a typed op, so its field comments are the document's.

// The field types a form may ask, in the order a builder offers them.
const (
	ShortText    = "short_text"
	LongText     = "long_text"
	Email        = "email"
	Phone        = "phone"
	URL          = "url"
	Number       = "number"
	Rating       = "rating"
	OpinionScale = "opinion_scale"
	YesNo        = "yes_no"
	MultiChoice  = "multiple_choice"
	Dropdown     = "dropdown"
	Date         = "date"
	Statement    = "statement"
)

// Types is every field type the service knows how to validate. Which of them a
// deployment offers, and each one's bound, is the platform's Settings.
var Types = []string{ShortText, LongText, Email, Phone, URL, Number, Rating, OpinionScale, YesNo, MultiChoice, Dropdown, Date, Statement}

// The kinds of form. A kind is a closed set: survey (an audience, a response
// window, identified or anonymous answering and scoring) is the next value it
// takes, and a kind added is a value added, never a field changed.
const (
	// Plain is a form whose every submission is a response of its own.
	Plain = "form"
	// Subscribe is a mailing-list form: it asks for an email, and one address is
	// one response however often it subscribes.
	Subscribe = "subscribe"
)

// Kinds is every kind a form may be.
var Kinds = []string{Plain, Subscribe}

// The identity policies a form answers under (Options.Identity).
const (
	// Claimed takes a typed address as the respondent's claim, filed unverified.
	Claimed = "claimed"
	// Verified takes only a signed-in respondent as anyone.
	Verified = "verified"
)

// Form is one form: what it asks, how it looks, and where its responses go.
type Form struct {
	// ID is the form's id: 15 lowercase hex characters, derived from the slug when
	// the form has one, so a slug names one form for good.
	ID string `json:"id"`
	// Slug is the form's own name within its org: lowercase letters, digits and
	// dashes, at most 64. Optional; once set it is the form's id, so it cannot change.
	Slug string `json:"slug,omitempty"`
	// PublicID is the address the form is answered at: "<org>.<slug or id>", as
	// GET /v1/form/public/{id} and POST /v1/form/public/{id}/responses take it.
	PublicID string `json:"public_id"`
	// Kind is form (every submission is a response, the default) or subscribe (a
	// mailing list: an email field is required and one address is one response).
	Kind string `json:"kind"`
	// Title is the form's name, as a respondent and the CRM timeline read it. At
	// most 200 characters.
	Title string `json:"title"`
	// Fields are the questions, in the order they are asked.
	Fields []Field `json:"fields"`
	// Welcome is the screen shown before the first question. Optional.
	Welcome *Screen `json:"welcome,omitempty"`
	// Thanks is the screen shown after a submission, or where the respondent is
	// sent. Optional.
	Thanks *Screen `json:"thanks,omitempty"`
	// Hidden names the values a page passes in when it embeds the form (a video
	// id, a plan): lowercase letters, digits and underscores, at most 32 each.
	Hidden []string `json:"hidden,omitempty"`
	// Settings is how the form is answered.
	Settings Options `json:"settings"`
	// Theme is the id of the theme the form is drawn in. Optional.
	Theme string `json:"theme,omitempty"`
	// Tags label the form for its org: at most 20, each at most 64 characters.
	Tags []string `json:"tags,omitempty"`
	// Sinks are where each completed response goes. Absent on a write, the
	// platform's defaults are taken.
	Sinks *Sinks `json:"sinks,omitempty"`
	// Created is when the form was made, RFC 3339.
	Created string `json:"created,omitempty"`
	// Updated is when it last changed, RFC 3339.
	Updated string `json:"updated,omitempty"`
}

// Field is one question.
type Field struct {
	// ID is the field's id: assigned when a write leaves it empty, and kept
	// across edits by sending it back.
	ID string `json:"id"`
	// Ref is the field's stable name, how an answer and a sink name it:
	// lowercase letters, digits, dash and underscore, at most 64, unique in the
	// form. Defaults to the id.
	Ref string `json:"ref"`
	// Type is one of short_text, long_text, email, phone, url, number, rating,
	// opinion_scale, yes_no, multiple_choice, dropdown, date and statement.
	Type string `json:"type"`
	// Title is the question as asked. At most 500 characters.
	Title string `json:"title"`
	// Description is the line under the question. At most 1000 characters.
	Description string `json:"description,omitempty"`
	// Required is whether a completed response must answer it. A statement is
	// never required.
	Required bool `json:"required,omitempty"`
	// Choices are what a multiple_choice or dropdown field offers, in order.
	Choices []Choice `json:"choices,omitempty"`
	// Multiple is whether a multiple_choice field takes several choices.
	Multiple bool `json:"multiple,omitempty"`
	// Steps is how many points a rating (3 to 10, default 5) or an opinion_scale
	// (5 to 11, default 11) has.
	Steps int `json:"steps,omitempty"`
	// Start is where an opinion_scale begins: 0 (the default) or 1.
	Start int `json:"start,omitempty"`
	// Labels name the ends and middle of an opinion_scale.
	Labels *Labels `json:"labels,omitempty"`
	// Validations bound an answer beyond its type.
	Validations *Validations `json:"validations,omitempty"`
}

// Choice is one option of a multiple_choice or dropdown field.
type Choice struct {
	// ID is the choice's id, assigned when empty.
	ID string `json:"id"`
	// Ref is how an answer names the choice; unique in the field, defaults to the id.
	Ref string `json:"ref"`
	// Label is the choice as shown. At most 200 characters.
	Label string `json:"label"`
}

// Labels name the left end, the middle and the right end of an opinion_scale.
type Labels struct {
	Left   string `json:"left,omitempty"`
	Center string `json:"center,omitempty"`
	Right  string `json:"right,omitempty"`
}

// Validations bound an answer beyond what its type allows.
type Validations struct {
	// MaxLength is the most characters a text answer holds, at most the
	// platform's bound for the type.
	MaxLength int `json:"max_length,omitempty"`
	// MinValue is the least a number answer may be.
	MinValue *float64 `json:"min_value,omitempty"`
	// MaxValue is the most a number answer may be.
	MaxValue *float64 `json:"max_value,omitempty"`
}

// Screen is the welcome or thank-you screen.
type Screen struct {
	// Title is the screen's heading. At most 200 characters.
	Title string `json:"title,omitempty"`
	// Description is the text under it. At most 2000 characters.
	Description string `json:"description,omitempty"`
	// Button is the button's label. At most 64 characters.
	Button string `json:"button,omitempty"`
	// Redirect is where a thank-you screen sends the respondent instead of
	// showing itself: an https address. Ignored on a welcome screen.
	Redirect string `json:"redirect,omitempty"`
}

// Options are how a form is answered.
type Options struct {
	// Visibility is public (answerable at its public address by anyone) or
	// private (not served there). Default public.
	Visibility string `json:"visibility"`
	// Language is the form's language, a tag like en or pt-BR. Default en.
	Language string `json:"language"`
	// Progress is whether the renderer shows a progress bar.
	Progress bool `json:"progress"`
	// Mode is one (a question at a time, the default) or all (every question at once).
	Mode string `json:"mode"`
	// Identity is who a response is from when nobody is signed in. claimed (the
	// default): an address typed into an email field is the respondent's CLAIM,
	// filed on the CRM marked unverified, on the person it names only when the CRM
	// already holds them and without changing them; it joins no list and is mailed
	// no welcome, and a list's consent is asked by the double opt-in instead.
	// verified: only a signed-in respondent (their token) is anyone; a typed
	// address is an answer and nothing more. A signed-in respondent is verified
	// under either.
	Identity string `json:"identity"`
}

// Sinks are where a completed response goes, beside the form's own responses.
type Sinks struct {
	// Events states form_viewed, form_started and form_submitted on the
	// deployment's event stream: the form's ids and the refs of the fields
	// answered, never an answer's text.
	Events bool `json:"events"`
	// CRM files the response on the timeline of the person it is from, or on the
	// org's own timeline when it names nobody. Absent, nothing is filed.
	CRM *CRM `json:"crm,omitempty"`
	// Forward mails every completed response to one address.
	Forward *Forward `json:"forward,omitempty"`
	// Welcome mails the respondent once, the first time their address answers.
	Welcome *Welcome `json:"welcome,omitempty"`
}

// CRM says which fields fill the person a response is filed on.
type CRM struct {
	// Email is the ref of the field whose answer is the person's address.
	// Empty, the first email field is. A signed-in respondent is always the
	// address their account holds.
	Email string `json:"email,omitempty"`
	// Name is the ref of the field whose answer is the person's name.
	Name string `json:"name,omitempty"`
	// Company is the ref of the field whose answer is the person's company.
	Company string `json:"company,omitempty"`
	// List is the CRM list the person joins, by name (a subscribe form's
	// mailing list). Empty joins none.
	List string `json:"list,omitempty"`
}

// Forward is the address every completed response is mailed to.
type Forward struct {
	// Email is the address. Required.
	Email string `json:"email"`
	// Name is who it is, for the greeting. Optional.
	Name string `json:"name,omitempty"`
}

// Welcome is the mail a respondent is sent once, when their address first answers.
type Welcome struct {
	// Subject is the mail's subject. At most 200 characters.
	Subject string `json:"subject"`
	// Body is the mail's text: plain text, a blank line a new paragraph. At most
	// 10000 characters.
	Body string `json:"body"`
}

// Theme is how a form is drawn.
type Theme struct {
	// ID is the theme's id.
	ID string `json:"id"`
	// Name is the theme's name in its org. At most 100 characters.
	Name string `json:"name"`
	// Colors are the theme's colours, each #rrggbb.
	Colors Colors `json:"colors"`
	// Font is the font family the form is set in: letters, digits, spaces and
	// dashes, at most 64. Empty is the renderer's own.
	Font string `json:"font,omitempty"`
	// Background is an https address of an image drawn behind the form. Optional.
	Background string `json:"background,omitempty"`
	// Created is when the theme was made, RFC 3339.
	Created string `json:"created,omitempty"`
	// Updated is when it last changed, RFC 3339.
	Updated string `json:"updated,omitempty"`
}

// Colors are a theme's colours, each #rrggbb.
type Colors struct {
	Question   string `json:"question"`
	Answer     string `json:"answer"`
	Button     string `json:"button"`
	Background string `json:"background"`
}

// Answer is what a response said to one field. Exactly one of text, number,
// boolean, choices and date is set, by the field's type: text for short_text,
// long_text, email, phone and url; number for number, rating and
// opinion_scale; boolean for yes_no; choices (the refs picked) for
// multiple_choice and dropdown; date (YYYY-MM-DD) for date.
type Answer struct {
	// Field is the ref of the field answered.
	Field string `json:"field"`
	// Type is the field's type. Set on a read; ignored on a write.
	Type    string   `json:"type,omitempty"`
	Text    *string  `json:"text,omitempty"`
	Number  *float64 `json:"number,omitempty"`
	Boolean *bool    `json:"boolean,omitempty"`
	Choices []string `json:"choices,omitempty"`
	Date    string   `json:"date,omitempty"`
}

// Respondent is who a response is from, as the service knows it.
type Respondent struct {
	// Kind is account (a signed-in person, from their token), email (an email
	// field's answer), visitor (the page's visitor id) or none.
	Kind string `json:"kind"`
	// Subject is the signed-in person's IAM subject.
	Subject string `json:"subject,omitempty"`
	// Email is the address the response is from: the account's, else the email
	// field's.
	Email string `json:"email,omitempty"`
	// PersonID is the CRM person the address is: the one key a response, a list
	// membership, an email event and an experiment share. Every event and
	// timeline entry the service states carries it, never the address.
	PersonID string `json:"person_id,omitempty"`
	// Verified is whether the respondent is who the response says: true only
	// for a signed-in person, from their token. A typed address is a claim.
	Verified bool `json:"verified"`
	// Visitor is the page's visitor id.
	Visitor string `json:"visitor,omitempty"`
}

// Response is one response to a form.
type Response struct {
	// ID is the response's id: derived from the form and the submission's token
	// (from the form and the address on a subscribe form), so a retried
	// submission is this same response.
	ID string `json:"id"`
	// Form is the form's id.
	Form string `json:"form"`
	// Token is the submission token the response was made under.
	Token string `json:"token,omitempty"`
	// Completed is whether the response was submitted whole; false is one saved
	// part way.
	Completed bool `json:"completed"`
	// LandedAt is when the respondent opened the form, RFC 3339, as their page said.
	LandedAt string `json:"landed_at,omitempty"`
	// SubmittedAt is when the response was completed, RFC 3339.
	SubmittedAt string `json:"submitted_at,omitempty"`
	// Duration is how many seconds lay between landing and submitting.
	Duration int `json:"duration,omitempty"`
	// Answers are the answers, in the form's field order.
	Answers []Answer `json:"answers"`
	// Hidden are the values the page passed in, by name.
	Hidden map[string]string `json:"hidden,omitempty"`
	// Respondent is who it is from.
	Respondent Respondent `json:"respondent"`
	// Path is the page the form was answered on.
	Path string `json:"path,omitempty"`
	// Source is web, or import for a response brought in from elsewhere.
	Source string `json:"source"`
	// External is an imported response's id where it came from.
	External string `json:"external,omitempty"`
	// Filed is whether a completed response is on the CRM's timeline. A filing
	// that failed is tried again on a schedule, and POST .../file tries it now.
	Filed bool `json:"filed"`
	// Filing is why the last filing failed; empty once it is filed.
	Filing string `json:"filing,omitempty"`
	// Created is when the record was first written, RFC 3339.
	Created string `json:"created,omitempty"`
	// Updated is when it last changed, RFC 3339.
	Updated string `json:"updated,omitempty"`
}

// Webhook is an address every completed response of a form is posted to.
type Webhook struct {
	// ID is the webhook's id.
	ID string `json:"id"`
	// Form is the form's id.
	Form string `json:"form"`
	// Tag names the webhook in its form. At most 64 characters.
	Tag string `json:"tag,omitempty"`
	// URL is the https address posted to.
	URL string `json:"url"`
	// Enabled is whether responses are posted.
	Enabled bool `json:"enabled"`
	// Secret is the signing secret, answered once: when the webhook is made or
	// its secret is set or rotated. Every delivery carries Hanzo-Signature:
	// t=<unix seconds>,sha256=<base64 of HMAC-SHA256(secret, "<seconds>." + body)>;
	// a receiver refuses one older than five minutes. It is kept in KMS and never
	// read back.
	Secret string `json:"secret,omitempty"`
	// Created is when the webhook was made, RFC 3339.
	Created string `json:"created,omitempty"`
	// Updated is when it last changed, RFC 3339.
	Updated string `json:"updated,omitempty"`
}

// Delivery is one response posted to one webhook, and how it went.
type Delivery struct {
	// ID is the delivery's id; the body carries it as event_id.
	ID string `json:"id"`
	// Webhook is the webhook's id.
	Webhook string `json:"webhook"`
	// Response is the response's id.
	Response string `json:"response"`
	// Status is delivered, retrying or failed.
	Status string `json:"status"`
	// Attempts is how many posts were made.
	Attempts int `json:"attempts"`
	// Code is the last HTTP status the address answered; 0 when it did not answer.
	Code int `json:"code,omitempty"`
	// Error is why the last attempt failed.
	Error string `json:"error,omitempty"`
	// Next is when the next attempt is due, RFC 3339; empty when none is.
	Next string `json:"next,omitempty"`
	// Created is when the first attempt was made, RFC 3339.
	Created string `json:"created,omitempty"`
	// Updated is when the last was, RFC 3339.
	Updated string `json:"updated,omitempty"`
}
