// The public half of /v1/form, as the service's model.go and public.go state it:
// a published form as its page draws it, a submission and its receipt.

/** A field's type. */
export type Type =
  | 'short_text'
  | 'long_text'
  | 'email'
  | 'phone'
  | 'url'
  | 'number'
  | 'rating'
  | 'opinion_scale'
  | 'yes_no'
  | 'multiple_choice'
  | 'dropdown'
  | 'date'
  | 'statement'

/** One option of a multiple_choice or dropdown field. */
export interface Choice {
  id: string
  /** How an answer names the choice. */
  ref: string
  label: string
}

/** The names of an opinion_scale's left end, middle and right end. */
export interface Labels {
  left?: string
  center?: string
  right?: string
}

/** Bounds on an answer beyond its type. */
export interface Validations {
  /** The most characters a text answer holds. */
  max_length?: number
  /** The least a number answer may be. */
  min_value?: number
  /** The most a number answer may be. */
  max_value?: number
}

/** One question. */
export interface Field {
  id: string
  /** The field's stable name; an answer names its field by it. */
  ref: string
  type: Type
  title: string
  description?: string
  required?: boolean
  choices?: Choice[]
  /** Whether a multiple_choice field takes several choices. */
  multiple?: boolean
  /** Points on a rating (3 to 10, default 5) or an opinion_scale (5 to 11, default 11). */
  steps?: number
  /** Where an opinion_scale begins: 0 (absent) or 1. */
  start?: number
  labels?: Labels
  validations?: Validations
}

/** The welcome or thank-you screen. */
export interface Screen {
  title?: string
  description?: string
  button?: string
  /** Where a thank-you screen sends the respondent: an https address. */
  redirect?: string
}

/** A theme's colours, each #rrggbb. */
export interface Colors {
  question: string
  answer: string
  button: string
  background: string
}

/** How a form is drawn. */
export interface Theme {
  id: string
  name: string
  colors: Colors
  /** A font family: letters, digits, spaces and dashes. */
  font?: string
  /** An https address of an image drawn behind the form. */
  background?: string
}

/** The settings a renderer reads. */
export interface Drawn {
  /** A language tag like en or pt-BR. */
  language: string
  /** Whether a progress bar is shown. */
  progress: boolean
  /** one: a question at a time; all: every question at once. */
  mode: 'one' | 'all'
}

/** A published form, as GET /v1/form/public/{publicId} answers it. */
export interface PublicForm {
  /** The public id, "<org>.<slug or id>". */
  id: string
  kind: 'form' | 'subscribe'
  title: string
  fields: Field[]
  welcome?: Screen
  thanks?: Screen
  /** The names of the values a page passes in. */
  hidden?: string[]
  settings: Drawn
  theme?: Theme
}

/** What a response says to one field: exactly one value, by the field's type. */
export interface Answer {
  /** The ref of the field answered. */
  field: string
  /** short_text, long_text, email, phone, url. */
  text?: string
  /** number, rating, opinion_scale. */
  number?: number
  /** yes_no. */
  boolean?: boolean
  /** multiple_choice, dropdown: the refs picked. */
  choices?: string[]
  /** date: YYYY-MM-DD. */
  date?: string
}

/** One respondent's answers, as POST /v1/form/public/{publicId}/responses takes them. */
export interface Submission {
  /** Idempotency: 8 to 64 of [A-Za-z0-9_-]. One token is one response. */
  token?: string
  answers: Answer[]
  /** The values the page passes in, by the names the form declares. */
  hidden?: Record<string, string>
  /** When the respondent opened the form, RFC 3339. */
  landed_at?: string
  /** false saves part way under the token; true or absent submits whole. */
  completed?: boolean
  /** The page's visitor id: [A-Za-z0-9_.-]{1,64}. */
  visitor?: string
  /** The page the form was answered on. */
  path?: string
  /** The field no person fills; a submission that fills it is refused. */
  website?: string
}

/** What a submission is answered: 201. */
export interface Receipt {
  id: string
  completed: boolean
  submitted_at?: string
  /** The form's thank-you screen, for a completed response. */
  thanks?: Screen
}
