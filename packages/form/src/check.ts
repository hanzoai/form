// Reading what a respondent put in a field, the way the service reads an answer
// (answer.go checkAnswer): the one value the field's type takes, inside its
// bounds. The service judges again and its word is final; this is the same
// judgement made before the round trip, so a respondent hears it at once.

import type { Answer, Field, PublicForm, Submission } from './types'

/**
 * What a respondent has put in a field before it is an answer: text for the
 * text types, number and date (as typed), a number for rating and
 * opinion_scale, a boolean for yes_no, the refs picked for multiple_choice, the
 * ref picked for dropdown.
 */
export type Value = string | number | boolean | string[] | undefined

/** One field read: its answer (null when left blank) and what is wrong ('' when nothing). */
export interface Read {
  answer: Answer | null
  problem: string
}

const phone = /^\+?[0-9 ().-]{3,32}$/
const day = /^(\d{4})-(\d{2})-(\d{2})$/
const quoting = /[ \t\r\n,;%"<>()[\]\\]/
// A control character but a line break or a tab.
const control = /[\u0000-\u0008\u000b-\u001f\u007f-\u009f]/
const breaks = /[\n\t]/

/** Reads one field's value as the service would read its answer. */
export function check(field: Field, value: Value): Read {
  const read = shape(field, value)
  if (!read.problem && !read.answer && field.required && field.type !== 'statement') {
    return { answer: null, problem: 'This question is required.' }
  }
  return read
}

function shape(field: Field, value: Value): Read {
  const blank = { answer: null, problem: '' }
  const wrong = (problem: string) => ({ answer: null, problem })
  const ref = field.ref
  switch (field.type) {
    case 'statement':
      return blank
    case 'short_text':
    case 'long_text':
    case 'email':
    case 'phone':
    case 'url': {
      let t = typeof value === 'string' ? value : ''
      if (field.type === 'long_text') t = t.replace(/\r\n/g, '\n')
      t = t.trim()
      if (!t) return blank
      if (control.test(t) || (field.type !== 'long_text' && breaks.test(t))) {
        return wrong(field.type === 'long_text' ? 'Use text without control characters.' : 'Use one line of text.')
      }
      const bound = field.validations?.max_length
      if (bound && [...t].length > bound) return wrong(`Use at most ${bound} characters.`)
      if (field.type === 'email' && !address(t)) return wrong('Enter an email address.')
      if (field.type === 'phone' && !(phone.test(t) && between(digits(t), 3, 20))) return wrong('Enter a phone number.')
      if (field.type === 'url' && !web(t)) return wrong('Enter a web address, starting with https://.')
      return { answer: { field: ref, text: t }, problem: '' }
    }
    case 'number':
    case 'rating':
    case 'opinion_scale': {
      if (value === undefined || (typeof value === 'string' && !value.trim())) return blank
      const n = typeof value === 'number' ? value : Number(String(value).trim())
      if (!Number.isFinite(n)) return wrong('Enter a number.')
      if (field.type === 'rating') {
        const hi = field.steps || 5
        if (!Number.isInteger(n) || !between(n, 1, hi)) return wrong(`Pick a whole number from 1 to ${hi}.`)
      } else if (field.type === 'opinion_scale') {
        const lo = field.start ?? 0
        const hi = lo + (field.steps || 11) - 1
        if (!Number.isInteger(n) || !between(n, lo, hi)) return wrong(`Pick a whole number from ${lo} to ${hi}.`)
      } else {
        const v = field.validations
        if (v?.min_value !== undefined && n < v.min_value) return wrong(`Enter at least ${v.min_value}.`)
        if (v?.max_value !== undefined && n > v.max_value) return wrong(`Enter at most ${v.max_value}.`)
      }
      return { answer: { field: ref, number: n }, problem: '' }
    }
    case 'yes_no':
      return typeof value === 'boolean' ? { answer: { field: ref, boolean: value }, problem: '' } : blank
    case 'multiple_choice':
    case 'dropdown': {
      const picked = (Array.isArray(value) ? value : typeof value === 'string' && value ? [value] : []).map((c) => c.trim())
      if (picked.length === 0) return blank
      if ((field.type === 'dropdown' || !field.multiple) && picked.length !== 1) return wrong('Pick one choice.')
      if (new Set(picked).size !== picked.length) return wrong('Pick each choice once.')
      if (!picked.every((c) => field.choices?.some((x) => x.ref === c))) return wrong('Pick from the choices offered.')
      return { answer: { field: ref, choices: picked }, problem: '' }
    }
    case 'date': {
      const d = typeof value === 'string' ? value.trim() : ''
      if (!d) return blank
      if (!calendar(d)) return wrong('Enter a date, YYYY-MM-DD.')
      return { answer: { field: ref, date: d }, problem: '' }
    }
  }
}

/** Reads every field of a form: the answers in field order, and each problem by ref. */
export function answers(form: PublicForm, values: Record<string, Value>): { answers: Answer[]; problems: Record<string, string> } {
  const out: Answer[] = []
  const problems: Record<string, string> = {}
  for (const field of form.fields) {
    const read = check(field, values[field.ref])
    if (read.problem) problems[field.ref] = read.problem
    else if (read.answer) out.push(read.answer)
  }
  return { answers: out, problems }
}

/** The page's facts a submission carries beside the answers. */
export interface Page {
  /** The idempotency token, minted once per form drawn. */
  token: string
  /** When the form was drawn, RFC 3339. */
  landed: string
  hidden?: Record<string, string>
  visitor?: string
  path?: string
  /** The honeypot's value: empty from a person. */
  website: string
}

/**
 * The completed submission a form's values make: the answers, and only the
 * hidden values the form declares.
 */
export function submission(form: PublicForm, values: Record<string, Value>, page: Page): Submission {
  const hidden: Record<string, string> = {}
  for (const name of form.hidden ?? []) {
    const v = page.hidden?.[name]
    if (v !== undefined) hidden[name] = String(v)
  }
  const out: Submission = {
    token: page.token,
    answers: answers(form, values).answers,
    hidden,
    landed_at: page.landed,
    completed: true,
    website: page.website,
  }
  if (page.visitor) out.visitor = page.visitor
  if (page.path) out.path = [...page.path].slice(0, 500).join('')
  return out
}

/** A fresh idempotency token: "f_" and 32 hex digits of a random UUID. */
export function mint(): string {
  return `f_${crypto.randomUUID().replace(/-/g, '')}`
}

// address mirrors the service's check.go address: one @, something before it,
// a dotted domain after it, and none of the characters that join or quote a
// second address.
function address(s: string): boolean {
  const at = s.indexOf('@')
  if (at <= 0) return false
  const domain = s.slice(at + 1)
  return (
    new TextEncoder().encode(s).length <= 320 &&
    !quoting.test(s) &&
    !domain.includes('@') &&
    domain.replace(/^\.+|\.+$/g, '').includes('.')
  )
}

function web(s: string): boolean {
  try {
    const u = new URL(s)
    return (u.protocol === 'https:' || u.protocol === 'http:') && u.hostname !== ''
  } catch {
    return false
  }
}

function calendar(s: string): boolean {
  const m = day.exec(s)
  if (!m) return false
  const [y, mo, d] = [Number(m[1]), Number(m[2]), Number(m[3])]
  const t = new Date(0)
  t.setUTCFullYear(y, mo - 1, d)
  return t.getUTCFullYear() === y && t.getUTCMonth() === mo - 1 && t.getUTCDate() === d
}

function digits(s: string): number {
  return s.replace(/[^0-9]/g, '').length
}

function between(n: number, lo: number, hi: number): boolean {
  return n >= lo && n <= hi
}
