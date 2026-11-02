import { describe, expect, it } from 'vitest'
import { answers, check, mint, submission } from './check'
import type { Field, PublicForm } from './types'

const f = (type: Field['type'], more: Partial<Field> = {}): Field => ({ id: type, ref: type, type, title: type, ...more })

describe('check', () => {
  it('leaves a blank optional field unanswered and a blank required one wrong', () => {
    expect(check(f('short_text'), '   ')).toEqual({ answer: null, problem: '' })
    expect(check(f('short_text', { required: true }), '').problem).toBe('This question is required.')
    expect(check(f('yes_no', { required: true }), undefined).problem).toBe('This question is required.')
    expect(check(f('statement', { required: true }), undefined)).toEqual({ answer: null, problem: '' })
  })

  it('trims text, keeps one line, and holds max_length in characters', () => {
    expect(check(f('short_text'), '  hi  ').answer).toEqual({ field: 'short_text', text: 'hi' })
    expect(check(f('short_text'), 'a\nb').problem).toBe('Use one line of text.')
    expect(check(f('long_text'), 'a\r\nb').answer).toEqual({ field: 'long_text', text: 'a\nb' })
    expect(check(f('long_text'), 'a\u0007b').problem).toBe('Use text without control characters.')
    const bound = f('short_text', { validations: { max_length: 3 } })
    expect(check(bound, 'ééé').problem).toBe('')
    expect(check(bound, 'éééé').problem).toBe('Use at most 3 characters.')
  })

  it('reads an email the way the service does', () => {
    const e = f('email')
    for (const ok of ['a@b.co', 'first.last+tag@sub.example.org', 'x@.example.com.']) expect(check(e, ok).problem).toBe('')
    for (const bad of ['a@b', '@b.co', 'a b@c.co', 'a@b@c.co', 'a,b@c.co', 'a@b.co;c@d.co', 'a@...'])
      expect(check(e, bad).problem).toBe('Enter an email address.')
  })

  it('reads a phone and a web address the way the service does', () => {
    expect(check(f('phone'), '+1 (415) 555-0100').problem).toBe('')
    expect(check(f('phone'), '12').problem).toBe('Enter a phone number.')
    expect(check(f('phone'), '+1 555 abc').problem).toBe('Enter a phone number.')
    expect(check(f('phone'), '1'.repeat(21)).problem).toBe('Enter a phone number.')
    expect(check(f('url'), 'https://hanzo.ai/x?y=1').problem).toBe('')
    expect(check(f('url'), 'http://hanzo.ai').problem).toBe('')
    expect(check(f('url'), 'ftp://hanzo.ai').problem).toBe('Enter a web address, starting with https://.')
    expect(check(f('url'), 'hanzo.ai').problem).toBe('Enter a web address, starting with https://.')
  })

  it('bounds numbers, ratings and scales', () => {
    const n = f('number', { validations: { min_value: 1, max_value: 10 } })
    expect(check(n, '2.5').answer).toEqual({ field: 'number', number: 2.5 })
    expect(check(n, '0').problem).toBe('Enter at least 1.')
    expect(check(n, '11').problem).toBe('Enter at most 10.')
    expect(check(n, 'abc').problem).toBe('Enter a number.')
    expect(check(f('rating'), 5).problem).toBe('')
    expect(check(f('rating'), 6).problem).toBe('Pick a whole number from 1 to 5.')
    expect(check(f('rating', { steps: 10 }), 10).problem).toBe('')
    expect(check(f('rating'), 2.5).problem).toBe('Pick a whole number from 1 to 5.')
    expect(check(f('opinion_scale'), 0).problem).toBe('')
    expect(check(f('opinion_scale'), 10).problem).toBe('')
    expect(check(f('opinion_scale'), 11).problem).toBe('Pick a whole number from 0 to 10.')
    expect(check(f('opinion_scale', { start: 1, steps: 5 }), 0).problem).toBe('Pick a whole number from 1 to 5.')
    expect(check(f('opinion_scale', { start: 1, steps: 5 }), 5).answer).toEqual({ field: 'opinion_scale', number: 5 })
  })

  it('takes choices the field offers, once each', () => {
    const choices = [
      { id: 'a', ref: 'a', label: 'A' },
      { id: 'b', ref: 'b', label: 'B' },
    ]
    const one = f('multiple_choice', { choices })
    const many = f('multiple_choice', { choices, multiple: true })
    expect(check(one, ['a']).answer).toEqual({ field: 'multiple_choice', choices: ['a'] })
    expect(check(one, ['a', 'b']).problem).toBe('Pick one choice.')
    expect(check(many, ['a', 'b']).answer).toEqual({ field: 'multiple_choice', choices: ['a', 'b'] })
    expect(check(many, ['a', 'a']).problem).toBe('Pick each choice once.')
    expect(check(many, ['c']).problem).toBe('Pick from the choices offered.')
    expect(check(f('dropdown', { choices }), 'b').answer).toEqual({ field: 'dropdown', choices: ['b'] })
    expect(check(f('dropdown', { choices }), '').answer).toBeNull()
  })

  it('takes a real calendar date', () => {
    expect(check(f('date'), '2026-02-28').answer).toEqual({ field: 'date', date: '2026-02-28' })
    expect(check(f('date'), '2026-02-30').problem).toBe('Enter a date, YYYY-MM-DD.')
    expect(check(f('date'), '2026-2-3').problem).toBe('Enter a date, YYYY-MM-DD.')
    expect(check(f('date'), '0050-01-01').problem).toBe('')
  })

  it('answers yes and no as booleans', () => {
    expect(check(f('yes_no'), false).answer).toEqual({ field: 'yes_no', boolean: false })
  })
})

const form: PublicForm = {
  id: 'hanzo.video-feedback',
  kind: 'form',
  title: 'Video feedback',
  fields: [
    f('statement', { ref: 'intro' }),
    f('rating', { ref: 'score', required: true }),
    f('long_text', { ref: 'comment' }),
    f('email', { ref: 'email' }),
  ],
  hidden: ['video'],
  settings: { language: 'en', progress: true, mode: 'one' },
}

describe('answers', () => {
  it('lists answers in field order and names each problem by ref', () => {
    expect(answers(form, { email: 'nope', comment: 'Loved it' })).toEqual({
      answers: [{ field: 'comment', text: 'Loved it' }],
      problems: { score: 'This question is required.', email: 'Enter an email address.' },
    })
  })
})

describe('submission', () => {
  it('is the shape POST /v1/form/public/{id}/responses takes', () => {
    const body = submission(
      form,
      { comment: ' Loved it ', score: 5, email: 'Z@Hanzo.ai' },
      {
        token: 'f_0123456789abcdef',
        landed: '2026-11-02T08:34:10.000Z',
        hidden: { video: '7mVTEAHHjPc', plan: 'pro' },
        visitor: 'v_k3j2h1',
        path: '/beta',
        website: '',
      },
    )
    expect(body).toEqual({
      token: 'f_0123456789abcdef',
      answers: [
        { field: 'score', number: 5 },
        { field: 'comment', text: 'Loved it' },
        { field: 'email', text: 'Z@Hanzo.ai' },
      ],
      hidden: { video: '7mVTEAHHjPc' },
      landed_at: '2026-11-02T08:34:10.000Z',
      completed: true,
      visitor: 'v_k3j2h1',
      path: '/beta',
      website: '',
    })
    expect(JSON.parse(JSON.stringify(body))).toEqual(body)
  })

  it('leaves out a visitor and a path it was not given, and bounds the path', () => {
    const body = submission(form, { score: 1 }, { token: 'f_0123456789', landed: 'x', website: '' })
    expect(body).not.toHaveProperty('visitor')
    expect(body).not.toHaveProperty('path')
    const long = submission(form, { score: 1 }, { token: 'f_0123456789', landed: 'x', website: '', path: `/${'p'.repeat(600)}` })
    expect([...(long.path ?? '')]).toHaveLength(500)
  })

  it('carries the honeypot as given', () => {
    expect(submission(form, {}, { token: 'f_0123456789', landed: 'x', website: 'spam' }).website).toBe('spam')
  })
})

describe('mint', () => {
  it('makes a fresh token of the shape the service takes', () => {
    const a = mint()
    expect(a).toMatch(/^[A-Za-z0-9_-]{8,64}$/)
    expect(mint()).not.toBe(a)
  })
})
