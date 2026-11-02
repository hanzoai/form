// Form draws a published form: a question at a time (mode one) or all at once
// (mode all), between its welcome and thank-you screens, and answers it through
// the client. A response is one per mount: the idempotency token is minted when
// the form is drawn, so a double click or a retry files one response. The
// thank-you screen is drawn only after the service answers 2xx; a refusal is
// said in the service's words and every answer stays where it was.

import { useEffect, useId, useRef, useState, type CSSProperties, type FormEvent, type KeyboardEvent, type ReactNode, type Ref } from 'react'
import { check, answers, mint, submission, type Value } from './check'
import { load, Problem, submit } from './client'
import { css } from './style'
import type { Field, PublicForm, Receipt, Theme } from './types'

/** What a page tells a form beside which form it is. */
export interface Options {
  /** The values the form declares in `hidden`, by name. Others are not sent. */
  hidden?: Record<string, string>
  /** The service's address; https://api.hanzo.ai when absent. */
  endpoint?: string
  /** An IAM bearer: the respondent is then the signed-in person. */
  token?: string
  /** The page's visitor id: [A-Za-z0-9_.-]{1,64}. */
  visitor?: string
  /** Called with the receipt once the service has filed the response. */
  onSubmit?: (receipt: Receipt) => void
  /** Leaves out the form's title above the questions. */
  hideHeaders?: boolean
  /** Leaves out the progress bar and the previous/next buttons. */
  hideFooter?: boolean
}

/** A form to draw, given whole or by its public id (then it is loaded). */
export type Props = Options & ({ form: PublicForm } | { publicId: string })

export function Form(props: Props) {
  const given = 'form' in props ? props.form : undefined
  const publicId = 'publicId' in props ? props.publicId : ''
  const { endpoint } = props
  const [fetched, setFetched] = useState<PublicForm | null>(null)
  const [failure, setFailure] = useState('')

  useEffect(() => {
    if (given) return
    let live = true
    setFetched(null)
    setFailure('')
    load(publicId, { endpoint }).then(
      (f) => live && setFetched(f),
      (e: unknown) => live && setFailure(said(e, 'The form could not be loaded. Check your connection and reload.')),
    )
    return () => {
      live = false
    }
  }, [given, publicId, endpoint])

  const form = given ?? fetched
  if (!form) {
    return (
      <Frame mode="one">
        <div className="body" aria-busy={!failure}>
          {failure ? (
            <p className="failure" role="alert">
              {failure}
            </p>
          ) : (
            <p>Loading…</p>
          )}
        </div>
      </Frame>
    )
  }
  return <Draw key={form.id} {...props} form={form} />
}

function Draw(props: Options & { form: PublicForm }) {
  const { form, hidden, endpoint, token, visitor, onSubmit, hideHeaders, hideFooter } = props
  const id = useId()
  const one = form.settings.mode !== 'all'
  const fields = form.fields
  const [step, setStep] = useState(form.welcome ? -1 : 0)
  const [values, setValues] = useState<Record<string, Value>>({})
  const [problems, setProblems] = useState<Record<string, string>>({})
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [receipt, setReceipt] = useState<Receipt | null>(null)
  const [nonce] = useState(mint)
  const [landed] = useState(() => new Date().toISOString())
  const flight = useRef(false)
  const moved = useRef(false)
  const root = useRef<HTMLDivElement>(null)
  const sheet = useRef<HTMLFormElement>(null)
  const trap = useRef<HTMLInputElement>(null)

  // Focus follows the respondent: the current question, screen or thanks. Not
  // on first draw, so an embedded form never pulls the page to itself.
  useEffect(() => {
    if (!moved.current) return
    const box = root.current?.querySelector<HTMLElement>('[data-current]')
    if (!box) return
    const target =
      box.querySelector<HTMLElement>('input:checked') ??
      box.querySelector<HTMLElement>('input, textarea, select') ??
      box.querySelector<HTMLElement>('button, [tabindex="-1"]')
    target?.focus()
  }, [step, receipt])

  const go = (to: number) => {
    moved.current = true
    setStep(to)
  }

  const put = (field: Field, value: Value) => {
    setValues((v) => ({ ...v, [field.ref]: value }))
    setProblems((p) => (p[field.ref] ? { ...p, [field.ref]: check(field, value).problem } : p))
  }

  const reach = (ref: string) => {
    const box = root.current?.querySelector<HTMLElement>(`[data-ref="${CSS.escape(ref)}"]`)
    box?.querySelector<HTMLElement>('input:checked, input, textarea, select, button')?.focus()
  }

  const send = async () => {
    if (flight.current || receipt) return
    const read = answers(form, values)
    const wrong = Object.keys(read.problems)
    if (wrong.length > 0) {
      setProblems(read.problems)
      if (one) go(fields.findIndex((f) => f.ref === wrong[0]))
      else reach(wrong[0])
      return
    }
    flight.current = true
    setBusy(true)
    setError('')
    const body = submission(form, values, {
      token: nonce,
      landed,
      hidden,
      visitor,
      path: typeof location === 'undefined' ? undefined : location.pathname,
      website: trap.current?.value ?? '',
    })
    let r: Receipt
    try {
      r = await submit(form.id, body, { endpoint, token })
    } catch (e) {
      setError(said(e, 'The form could not be sent. Your answers are kept; try again.'))
      return
    } finally {
      flight.current = false
      setBusy(false)
    }
    moved.current = true
    setReceipt(r)
    onSubmit?.(r)
    const to = https(r.thanks?.redirect)
    if (to) window.location.assign(to)
  }

  const advance = (e: FormEvent) => {
    e.preventDefault()
    if (receipt) return
    if (step < 0) return go(0)
    if (one) {
      const field = fields[step]
      if (field) {
        const read = check(field, values[field.ref])
        if (read.problem) {
          setProblems((p) => ({ ...p, [field.ref]: read.problem }))
          return
        }
      }
      if (step < fields.length - 1) return go(step + 1)
    }
    void send()
  }

  // Enter moves on: to the next question, and from the last one it submits.
  // In a long answer Shift+Enter is a new line, and with every question shown
  // so is Enter. A digit picks a rating, a point of a scale, a choice, or yes
  // (1) and no (2). Escape is the browser's: nothing is cleared.
  const keys = (e: KeyboardEvent<HTMLFormElement>) => {
    if (e.nativeEvent.isComposing || e.altKey || e.ctrlKey || e.metaKey) return
    const el = e.target as HTMLElement
    const tag = el.tagName
    const ref = el.closest<HTMLElement>('[data-ref]')?.dataset.ref
    if (e.key === 'Enter') {
      if (e.shiftKey || tag === 'BUTTON' || tag === 'A' || (tag === 'TEXTAREA' && !one)) return
      e.preventDefault()
      const at = fields.findIndex((f) => f.ref === ref)
      if (!one && at >= 0 && at < fields.length - 1) reach(fields[at + 1].ref)
      else sheet.current?.requestSubmit()
      return
    }
    if (!/^[0-9]$/.test(e.key)) return
    if (tag === 'TEXTAREA' || tag === 'SELECT') return
    if (tag === 'INPUT' && !['radio', 'checkbox'].includes((el as HTMLInputElement).type)) return
    const field = fields.find((f) => f.ref === ref)
    const picked = field && press(field, values[field.ref], Number(e.key))
    if (!field || !picked) return
    e.preventDefault()
    put(field, picked.value)
    // Focus goes with the pick, as an arrow key would take it.
    el.closest('[data-ref]')?.querySelectorAll<HTMLInputElement>('input')[picked.at]?.focus()
  }

  const counted = fields.filter((f) => f.type !== 'statement')
  const done = counted.filter((f) => check(f, values[f.ref]).answer).length
  const pct = receipt ? 100 : counted.length ? Math.round((done / counted.length) * 100) : 0
  const asking = !receipt && step >= 0
  const shown = one ? fields.slice(step, step + 1) : fields
  const thanks = receipt?.thanks
  const away = https(thanks?.redirect)

  let body: ReactNode
  if (receipt) {
    body = (
      <div className="screen" data-current="">
        <h2 tabIndex={-1}>{thanks?.title || 'Thank you.'}</h2>
        {thanks?.description && <p>{thanks.description}</p>}
        {away && (
          <a className="go" href={away}>
            {thanks?.button || 'Continue'}
          </a>
        )}
      </div>
    )
  } else if (step < 0 && form.welcome) {
    const w = form.welcome
    body = (
      <div className="screen" data-current="">
        <h2>{w.title || form.title}</h2>
        {w.description && <p>{w.description}</p>}
        <button type="button" className="go" onClick={() => go(0)}>
          {w.button || 'Start'}
        </button>
      </div>
    )
  } else {
    body = (
      <>
        {shown.map((field) => (
          <Question
            key={field.id || field.ref}
            id={id}
            field={field}
            value={values[field.ref]}
            problem={problems[field.ref] ?? ''}
            put={put}
            one={one}
            label={step >= fields.length - 1 ? (busy ? 'Sending…' : 'Submit') : field.type === 'statement' ? 'Continue' : 'OK'}
            busy={busy}
          />
        ))}
        <div>
          <p className="failure" aria-live="assertive">
            {error}
          </p>
          {!one && (
            <div className="actions">
              <button type="submit" className="go" aria-disabled={busy || undefined}>
                {busy ? 'Sending…' : 'Submit'}
              </button>
            </div>
          )}
        </div>
      </>
    )
  }

  return (
    <Frame theme={form.theme} mode={one ? 'one' : 'all'} language={form.settings.language} box={root}>
      <form ref={sheet} noValidate onSubmit={advance} onKeyDown={keys} aria-busy={busy || undefined}>
        {!hideHeaders && <p className="head">{form.title}</p>}
        <div className="body" data-current={!one && asking ? '' : undefined}>
          {body}
        </div>
        <input ref={trap} className="trap" type="text" name="website" tabIndex={-1} autoComplete="off" aria-hidden="true" defaultValue="" />
        {!hideFooter && (form.settings.progress || (one && asking && fields.length > 1)) && (
          <div className="foot">
            {form.settings.progress && (
              <div className="bar" role="progressbar" aria-label="Progress" aria-valuemin={0} aria-valuemax={100} aria-valuenow={pct}>
                <span style={{ width: `${pct}%` }} />
              </div>
            )}
            {one && asking && fields.length > 1 && (
              <div className="nav">
                <button type="button" aria-label="Previous question" disabled={step === 0} onClick={() => go(step - 1)}>
                  <svg viewBox="0 0 24 24" aria-hidden="true">
                    <path d="M6 15l6-6 6 6" />
                  </svg>
                </button>
                <button type="button" aria-label="Next question" disabled={step >= fields.length - 1} onClick={() => sheet.current?.requestSubmit()}>
                  <svg viewBox="0 0 24 24" aria-hidden="true">
                    <path d="M6 9l6 6 6-6" />
                  </svg>
                </button>
              </div>
            )}
          </div>
        )}
      </form>
    </Frame>
  )
}

interface Asked {
  id: string
  field: Field
  value: Value
  problem: string
  put: (field: Field, value: Value) => void
  one: boolean
  label: string
  busy: boolean
}

const inputs: Partial<Record<Field['type'], { type: string; mode?: 'email' | 'tel' | 'url' | 'decimal'; fill?: string; hint?: string }>> = {
  short_text: { type: 'text', hint: 'Type your answer here' },
  email: { type: 'email', mode: 'email', fill: 'email', hint: 'name@example.com' },
  phone: { type: 'tel', mode: 'tel', fill: 'tel' },
  url: { type: 'url', mode: 'url', fill: 'url', hint: 'https://' },
  number: { type: 'text', mode: 'decimal' },
  date: { type: 'date' },
}

function Question({ id, field, value, problem, put, one, label, busy }: Asked) {
  const input = `${id}-${field.ref}`
  const desc = `${input}-d`
  const err = `${input}-e`
  const described = field.description ? `${desc} ${err}` : err
  const title = (
    <>
      {field.title}
      {field.required && (
        <>
          <span aria-hidden="true"> *</span>
          <span className="sr"> (required)</span>
        </>
      )}
    </>
  )
  const note = field.description ? (
    <p className="desc" id={desc}>
      {field.description}
    </p>
  ) : null
  const invalid = problem ? true : undefined
  const text = typeof value === 'string' ? value : ''

  let body: ReactNode
  switch (field.type) {
    case 'statement':
      body = (
        <>
          <p className="title">{field.title}</p>
          {note}
        </>
      )
      break
    case 'long_text':
      body = (
        <>
          <label className="title" htmlFor={input}>
            {title}
          </label>
          {note}
          <textarea
            className="text"
            id={input}
            name={field.ref}
            rows={4}
            value={text}
            maxLength={field.validations?.max_length}
            placeholder={one ? 'Type your answer here. Shift+Enter for a new line.' : 'Type your answer here'}
            aria-invalid={invalid}
            aria-describedby={described}
            onChange={(e) => put(field, e.target.value)}
          />
        </>
      )
      break
    case 'dropdown':
      body = (
        <>
          <label className="title" htmlFor={input}>
            {title}
          </label>
          {note}
          <select
            className="text"
            id={input}
            name={field.ref}
            value={text}
            aria-invalid={invalid}
            aria-describedby={described}
            onChange={(e) => put(field, e.target.value)}
          >
            <option value="">Select an option</option>
            {field.choices?.map((c) => (
              <option key={c.ref} value={c.ref}>
                {c.label}
              </option>
            ))}
          </select>
        </>
      )
      break
    case 'rating': {
      const hi = field.steps || 5
      const picked = typeof value === 'number' ? value : 0
      body = (
        <fieldset role="radiogroup" aria-invalid={invalid} aria-describedby={described}>
          <legend className="title">{title}</legend>
          {note}
          <div className="stars">
            {range(1, hi).map((n) => (
              <label key={n} className="star" data-on={n <= picked}>
                <input type="radio" name={input} checked={picked === n} aria-label={`${n} of ${hi}`} onChange={() => put(field, n)} />
                <svg viewBox="0 0 24 24" aria-hidden="true">
                  <path d="M12 2.8l2.8 5.7 6.3.9-4.6 4.4 1.1 6.2L12 17l-5.6 3 1.1-6.2-4.6-4.4 6.3-.9z" />
                </svg>
                <span className="n" aria-hidden="true">
                  {n}
                </span>
              </label>
            ))}
          </div>
        </fieldset>
      )
      break
    }
    case 'opinion_scale': {
      const lo = field.start ?? 0
      const hi = lo + (field.steps || 11) - 1
      const l = field.labels
      body = (
        <fieldset role="radiogroup" aria-invalid={invalid} aria-describedby={described}>
          <legend className="title">{title}</legend>
          {note}
          <div className="scale">
            {range(lo, hi).map((n) => (
              <label key={n} className="cell" data-on={value === n}>
                <input type="radio" name={input} checked={value === n} onChange={() => put(field, n)} />
                {n}
              </label>
            ))}
          </div>
          {l && (l.left || l.center || l.right) && (
            <div className="ends">
              <span>{l.left && `${lo}: ${l.left}`}</span>
              <span>{l.center}</span>
              <span>{l.right && `${hi}: ${l.right}`}</span>
            </div>
          )}
        </fieldset>
      )
      break
    }
    case 'yes_no':
    case 'multiple_choice': {
      const many = field.type === 'multiple_choice' && !!field.multiple
      const options: { key: string; label: string; on: boolean; pick: () => void }[] =
        field.type === 'yes_no'
          ? [
              { key: 'yes', label: 'Yes', on: value === true, pick: () => put(field, true) },
              { key: 'no', label: 'No', on: value === false, pick: () => put(field, false) },
            ]
          : (field.choices ?? []).map((c) => {
              const picked = Array.isArray(value) ? value : []
              const on = picked.includes(c.ref)
              return {
                key: c.ref,
                label: c.label,
                on,
                pick: () => put(field, many ? (on ? picked.filter((x) => x !== c.ref) : [...picked, c.ref]) : [c.ref]),
              }
            })
      body = (
        <fieldset role={many ? 'group' : 'radiogroup'} aria-invalid={invalid} aria-describedby={described}>
          <legend className="title">{title}</legend>
          {note}
          <div className="tiles">
            {options.map((o, i) => (
              <label key={o.key} className="tile" data-on={o.on}>
                <input type={many ? 'checkbox' : 'radio'} name={input} checked={o.on} onChange={o.pick} />
                {i < 9 && (
                  <span className="key" aria-hidden="true">
                    {i + 1}
                  </span>
                )}
                {o.label}
              </label>
            ))}
          </div>
        </fieldset>
      )
      break
    }
    default: {
      const kind = inputs[field.type] ?? { type: 'text' }
      const v = field.validations
      body = (
        <>
          <label className="title" htmlFor={input}>
            {title}
          </label>
          {note}
          <input
            className="text"
            id={input}
            name={field.ref}
            type={kind.type}
            inputMode={kind.mode}
            autoComplete={kind.fill ?? 'off'}
            placeholder={kind.hint}
            value={text}
            maxLength={v?.max_length}
            aria-invalid={invalid}
            aria-describedby={described}
            onChange={(e) => put(field, e.target.value)}
          />
        </>
      )
    }
  }

  return (
    <div className="q" data-ref={field.ref} data-current={one ? '' : undefined}>
      {body}
      <p className="problem" id={err} aria-live="polite">
        {problem}
      </p>
      {one && (
        <div className="actions">
          <button type="submit" className="go" aria-disabled={busy || undefined}>
            {label}
          </button>
          <span className="hint" aria-hidden="true">
            press <b>Enter ↵</b>
          </span>
        </div>
      )}
    </div>
  )
}

// press is what a digit key picks in a field: the new value and the index of
// the control that shows it; undefined when it picks nothing there. 0 is 10
// where a scale reaches 10 and does not start at 0.
function press(field: Field, current: Value, d: number): { value: Value; at: number } | undefined {
  switch (field.type) {
    case 'rating': {
      const n = d === 0 ? 10 : d
      return n <= (field.steps || 5) ? { value: n, at: n - 1 } : undefined
    }
    case 'opinion_scale': {
      const lo = field.start ?? 0
      const hi = lo + (field.steps || 11) - 1
      const n = d === 0 && lo > 0 ? 10 : d
      return n >= lo && n <= hi ? { value: n, at: n - lo } : undefined
    }
    case 'yes_no':
      return d === 1 ? { value: true, at: 0 } : d === 2 ? { value: false, at: 1 } : undefined
    case 'multiple_choice': {
      const c = d > 0 ? field.choices?.[d - 1] : undefined
      if (!c) return undefined
      if (!field.multiple) return { value: [c.ref], at: d - 1 }
      const picked = Array.isArray(current) ? current : []
      return { value: picked.includes(c.ref) ? picked.filter((x) => x !== c.ref) : [...picked, c.ref], at: d - 1 }
    }
  }
  return undefined
}

interface Framed {
  theme?: Theme
  mode: 'one' | 'all'
  language?: string
  box?: Ref<HTMLDivElement>
  children: ReactNode
}

function Frame({ theme, mode, language, box, children }: Framed) {
  return (
    <div ref={box} className="hzform" data-mode={mode} lang={language && tag.test(language) ? language : undefined} style={paint(theme)}>
      <style>{css}</style>
      <div className="layer" aria-hidden="true" />
      <div className="scroll">{children}</div>
    </div>
  )
}

const hex = /^#[0-9a-fA-F]{6}$/
const family = /^[A-Za-z0-9 -]{1,64}$/
const tag = /^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8})*$/

// paint is the theme as the stylesheet's custom properties; a value that is not
// the shape the service writes is left for the dark default.
function paint(theme?: Theme): CSSProperties {
  const c = theme?.colors
  const color = (v: string | undefined, fallback: string) => (v && hex.test(v) ? v : fallback)
  const bg = color(c?.background, '#0a0a0a')
  const button = color(c?.button, '#fafafa')
  const image = https(theme?.background)
  const style: Record<string, string> = {
    '--hzform-question': color(c?.question, '#fafafa'),
    '--hzform-answer': color(c?.answer, '#e4e4e7'),
    '--hzform-button': button,
    '--hzform-ink': light(button) ? '#0a0a0a' : '#ffffff',
    '--hzform-bg': bg,
    '--hzform-image': image && !/["\\\r\n]/.test(image) ? `url("${image}")` : 'none',
    colorScheme: light(bg) ? 'light' : 'dark',
  }
  if (theme?.font && family.test(theme.font.trim())) style.fontFamily = `"${theme.font.trim()}", system-ui, sans-serif`
  return style as CSSProperties
}

// light is whether a #rrggbb colour reads better under black than white: its
// relative luminance (WCAG 2) above the point where the two contrasts meet.
function light(color: string): boolean {
  const [r, g, b] = [1, 3, 5].map((i) => {
    const v = parseInt(color.slice(i, i + 2), 16) / 255
    return v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4
  })
  return 0.2126 * r + 0.7152 * g + 0.0722 * b > 0.179
}

function https(url?: string): string {
  if (!url) return ''
  try {
    return new URL(url).protocol === 'https:' ? url : ''
  } catch {
    return ''
  }
}

function range(lo: number, hi: number): number[] {
  return Array.from({ length: Math.max(0, hi - lo + 1) }, (_, i) => lo + i)
}

// said is a failure in words: the service's own, else the fallback.
function said(e: unknown, fallback: string): string {
  if (!(e instanceof Problem)) return fallback
  const s = e.message.replace(/^form: /, '')
  return s.charAt(0).toUpperCase() + s.slice(1)
}
