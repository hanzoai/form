// The renderer in a real browser: the embed draws a published form, the
// service is api.hanzo.ai answered by routes, and each spec reads what a
// respondent sees and what the service is sent.

import { fileURLToPath } from 'node:url'
import { expect, test, type Page, type Route } from '@playwright/test'
import { build } from 'esbuild'
import type { PublicForm, Submission } from '../src/index'

const site = 'https://hanzo.test'
const api = 'https://api.hanzo.ai/v1/form/public'
const cors = {
  'access-control-allow-origin': site,
  'access-control-allow-headers': 'content-type, authorization, accept',
  'access-control-allow-methods': 'GET, POST',
}

const sample: PublicForm = {
  id: 'hanzo.sample',
  kind: 'form',
  title: 'Tell us about the beta',
  fields: [
    { id: 'f1', ref: 'score', type: 'rating', title: 'How was the beta?', required: true, steps: 5 },
    {
      id: 'f2',
      ref: 'comment',
      type: 'long_text',
      title: 'What should we fix first?',
      description: 'A sentence or two is plenty.',
      validations: { max_length: 2000 },
    },
    { id: 'f3', ref: 'email', type: 'email', title: 'Your email', required: true },
    {
      id: 'f4',
      ref: 'uses',
      type: 'multiple_choice',
      title: 'What do you use?',
      multiple: true,
      choices: [
        { id: 'c1', ref: 'chat', label: 'Chat' },
        { id: 'c2', ref: 'dev', label: 'Dev' },
        { id: 'c3', ref: 'studio', label: 'Studio' },
      ],
    },
    { id: 'f5', ref: 'again', type: 'yes_no', title: 'Would you use it again?', required: true },
  ],
  thanks: { title: 'Thank you.', description: 'We read every answer.' },
  hidden: ['video'],
  settings: { language: 'en', progress: true, mode: 'all' },
  theme: {
    id: 't1',
    name: 'Dusk',
    colors: { question: '#f5f3ff', answer: '#c4b5fd', button: '#8b5cf6', background: '#1e1036' },
    font: 'Inter',
  },
}

const steps: PublicForm = {
  ...sample,
  id: 'hanzo.steps',
  welcome: { title: 'Two minutes on the beta', description: 'Five questions.', button: 'Begin' },
  thanks: { title: 'Thank you.', redirect: `${site}/done` },
  settings: { language: 'en', progress: true, mode: 'one' },
}

let script = ''

test.beforeAll(async () => {
  const out = await build({
    entryPoints: [fileURLToPath(new URL('./page.ts', import.meta.url))],
    bundle: true,
    write: false,
    format: 'iife',
    target: 'es2022',
    jsx: 'automatic',
    define: { 'process.env.NODE_ENV': '"production"' },
    logLevel: 'silent',
  })
  script = out.outputFiles[0].text
})

type Answer = (route: Route) => Promise<void>

const created: Answer = (route) =>
  route.fulfill({
    status: 201,
    headers: cors,
    json: { id: 'r_1', completed: true, submitted_at: '2026-11-02T08:35:00Z', thanks: (route.request().url().includes('steps') ? steps : sample).thanks },
  })

// open draws the form at https://hanzo.test/?id=<id>, the service answering GET
// with the form and POST through `answer`; every POST body lands in `posts`.
async function open(page: Page, form: PublicForm, posts: Submission[] = [], answer: Answer = created) {
  await page.route(`${site}/**`, (route) => {
    const path = new URL(route.request().url()).pathname
    if (path === '/page.js') return route.fulfill({ contentType: 'text/javascript', body: script })
    const body =
      path === '/done'
        ? '<!doctype html><title>done</title><p>done</p>'
        : '<!doctype html><html lang="en"><head><meta name="viewport" content="width=device-width,initial-scale=1"><title>form</title>' +
          '<style>html,body{margin:0;min-height:100%;background:#000}#root{min-height:100vh}</style></head>' +
          '<body><div id="root"></div><script src="/page.js"></script></body></html>'
    return route.fulfill({ contentType: 'text/html', body })
  })
  await page.route(`${api}/${form.id}`, (route) => route.fulfill({ headers: cors, json: form }))
  await page.route(`${api}/${form.id}/responses`, async (route) => {
    if (route.request().method() === 'OPTIONS') return route.fulfill({ status: 204, headers: cors })
    posts.push(route.request().postDataJSON() as Submission)
    await answer(route)
  })
  await page.goto(`${site}/?id=${form.id}`)
}

function later() {
  let open = () => {}
  const gate = new Promise<void>((resolve) => (open = resolve))
  return { gate, open }
}

const star = (page: Page, n: number) => page.locator('label.star', { has: page.getByRole('radio', { name: `${n} of 5` }) })
const tile = (page: Page, label: string) => page.locator('label.tile', { hasText: label })

async function fill(page: Page) {
  await star(page, 4).click()
  await page.getByLabel('What should we fix first?').fill('The export button.')
  await page.getByLabel(/Your email/).fill('z@hanzo.ai')
  await tile(page, 'Chat').click()
  await tile(page, 'Dev').click()
  await tile(page, 'Yes').click()
}

test('draws the sample form at desktop width, themed', async ({ page }, info) => {
  await page.setViewportSize({ width: 1280, height: 900 })
  await open(page, sample)
  await expect(page.getByText('Tell us about the beta')).toBeVisible()
  await expect(page.getByRole('radiogroup', { name: /How was the beta\?/ })).toBeVisible()
  await expect(page.getByLabel('What should we fix first?')).toHaveJSProperty('tagName', 'TEXTAREA')
  await expect(page.getByLabel(/Your email/)).toHaveAttribute('type', 'email')
  await expect(page.getByRole('group', { name: 'What do you use?' })).toBeVisible()
  await expect(page.getByRole('radiogroup', { name: /Would you use it again\?/ })).toBeVisible()
  await expect(page.getByRole('progressbar', { name: 'Progress' })).toHaveAttribute('aria-valuenow', '0')

  const look = await page.evaluate(() => {
    const css = (sel: string) => getComputedStyle(document.querySelector(sel) as Element)
    return {
      ground: css('.hzform .layer').backgroundColor,
      button: css('.hzform .go').backgroundColor,
      ink: css('.hzform .go').color,
      question: css('.hzform .title').color,
      font: css('.hzform').fontFamily,
      lang: document.querySelector('.hzform')?.getAttribute('lang'),
    }
  })
  expect(look).toEqual({
    ground: 'rgb(30, 16, 54)',
    button: 'rgb(139, 92, 246)',
    ink: 'rgb(10, 10, 10)',
    question: 'rgb(245, 243, 255)',
    font: 'Inter, system-ui, sans-serif',
    lang: 'en',
  })

  const trap = page.locator('input[name=website]')
  await expect(trap).toHaveAttribute('tabindex', '-1')
  await expect(trap).toHaveAttribute('aria-hidden', 'true')
  await expect(trap).toHaveAttribute('autocomplete', 'off')
  expect(await trap.evaluate((e) => e.getBoundingClientRect().width)).toBeLessThanOrEqual(1)

  await page.screenshot({ path: info.outputPath('form-desktop.png'), fullPage: true, animations: 'disabled' })
})

test('fits a phone: no sideways scroll at 375 and 360, every target 44px', async ({ page }, info) => {
  await page.setViewportSize({ width: 375, height: 812 })
  await open(page, sample)
  await expect(page.getByText('How was the beta?')).toBeVisible()
  for (const width of [375, 360]) {
    await page.setViewportSize({ width, height: 812 })
    const past = await page.evaluate(() =>
      [...document.querySelectorAll('.hzform *')]
        .filter((e) => !e.classList.contains('trap') && !e.classList.contains('sr') && e.getBoundingClientRect().right > innerWidth + 0.5)
        .map((e) => `${e.tagName}.${e.className}`),
    )
    expect(past, `${width}px`).toEqual([])
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    const small = await page.evaluate(() =>
      [...document.querySelectorAll<HTMLElement>('.hzform .tile, .hzform .star, .hzform .cell, .hzform .go, .hzform .nav button, .hzform .text')]
        .map((e) => e.getBoundingClientRect())
        .filter((r) => r.width < 44 || r.height < 44)
        .map((r) => `${r.width}x${r.height}`),
    )
    expect(small, `${width}px`).toEqual([])
    if (width === 375) await page.screenshot({ path: info.outputPath('form-375.png'), fullPage: true, animations: 'disabled' })
  }
})

test('says thank you only after a 201, with the submission the service takes', async ({ page }, info) => {
  await page.setViewportSize({ width: 1280, height: 900 })
  const posts: Submission[] = []
  const hold = later()
  await open(page, sample, posts, async (route) => {
    await hold.gate
    await created(route)
  })
  const opened = Date.now()
  await fill(page)
  await expect(page.getByRole('progressbar', { name: 'Progress' })).toHaveAttribute('aria-valuenow', '100')
  await page.getByRole('button', { name: 'Submit' }).click()
  await expect(page.getByRole('button', { name: 'Sending…' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Thank you.' })).toHaveCount(0)
  hold.open()
  await expect(page.getByRole('heading', { name: 'Thank you.' })).toBeVisible()
  await expect(page.getByText('We read every answer.')).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Thank you.' })).toBeFocused()

  expect(posts).toHaveLength(1)
  const body = posts[0]
  expect(body.token).toMatch(/^[A-Za-z0-9_-]{8,64}$/)
  expect(Date.parse(body.landed_at ?? '')).toBeGreaterThan(opened - 60_000)
  expect(Date.parse(body.landed_at ?? '')).toBeLessThanOrEqual(Date.now())
  expect(body).toEqual({
    token: body.token,
    landed_at: body.landed_at,
    answers: [
      { field: 'score', number: 4 },
      { field: 'comment', text: 'The export button.' },
      { field: 'email', text: 'z@hanzo.ai' },
      { field: 'uses', choices: ['chat', 'dev'] },
      { field: 'again', boolean: true },
    ],
    hidden: { video: '7mVTEAHHjPc' },
    completed: true,
    path: '/',
    website: '',
  })
  expect(await page.evaluate(() => window.receipt)).toMatchObject({ id: 'r_1', completed: true })
  await page.screenshot({ path: info.outputPath('form-thanks.png'), fullPage: true, animations: 'disabled' })
})

test('a double click is one response', async ({ page }) => {
  const posts: Submission[] = []
  const hold = later()
  await open(page, sample, posts, async (route) => {
    await hold.gate
    await created(route)
  })
  await fill(page)
  const submit = page.getByRole('button', { name: 'Submit' })
  await submit.dblclick()
  await page.getByRole('button', { name: 'Sending…' }).click({ force: true })
  hold.open()
  await expect(page.getByRole('heading', { name: 'Thank you.' })).toBeVisible()
  expect(posts).toHaveLength(1)
})

test("a refusal says the service's words and keeps every answer", async ({ page }) => {
  const posts: Submission[] = []
  let refuse = true
  await open(page, sample, posts, (route) =>
    refuse
      ? route.fulfill({
          status: 400,
          headers: { ...cors, 'content-type': 'application/problem+json' },
          body: JSON.stringify({ type: 'about:blank', title: 'Bad Request', status: 400, detail: 'form: email: is not an email address' }),
        })
      : route.abort('internetdisconnected'),
  )
  await fill(page)
  await page.getByRole('button', { name: 'Submit' }).click()
  await expect(page.locator('.failure')).toHaveText('Email: is not an email address')
  await expect(page.locator('.failure')).toHaveAttribute('aria-live', 'assertive')
  await expect(page.getByRole('heading', { name: 'Thank you.' })).toHaveCount(0)
  await expect(page.getByLabel(/Your email/)).toHaveValue('z@hanzo.ai')
  await expect(page.getByRole('radio', { name: '4 of 5' })).toBeChecked()

  refuse = false
  await page.getByRole('button', { name: 'Submit' }).click()
  await expect(page.locator('.failure')).toHaveText('The form could not be sent. Your answers are kept; try again.')
  await expect(page.getByLabel('What should we fix first?')).toHaveValue('The export button.')
  expect(posts).toHaveLength(2)
  expect(posts[1].token).toBe(posts[0].token)
})

test('checks answers before sending, as the service would', async ({ page }) => {
  const posts: Submission[] = []
  await open(page, sample, posts)
  await page.getByRole('button', { name: 'Submit' }).click()
  await expect(page.locator('.problem', { hasText: 'This question is required.' })).toHaveCount(3)
  await expect(page.getByRole('radio', { name: '1 of 5' })).toBeFocused()
  await page.getByLabel(/Your email/).fill('nope')
  await page.getByRole('button', { name: 'Submit' }).click()
  await expect(page.getByLabel(/Your email/)).toHaveAttribute('aria-invalid', 'true')
  const said = page.locator('[data-ref=email] .problem')
  await expect(said).toHaveText('Enter an email address.')
  await expect(said).toHaveAttribute('aria-live', 'polite')
  await page.getByLabel(/Your email/).fill('z@hanzo.ai')
  await expect(said).toHaveText('')

  // With every question shown, Enter moves to the next one; a long answer takes a new line.
  await page.getByLabel(/Your email/).press('Enter')
  await expect(page.getByRole('checkbox', { name: 'Chat' })).toBeFocused()
  await page.getByLabel('What should we fix first?').fill('one')
  await page.getByLabel('What should we fix first?').press('Enter')
  await expect(page.getByLabel('What should we fix first?')).toHaveValue('one\n')
  expect(posts).toHaveLength(0)
})

test('one question at a time, by keyboard', async ({ page }, info) => {
  await page.setViewportSize({ width: 375, height: 812 })
  const posts: Submission[] = []
  await open(page, steps, posts)
  await expect(page.getByRole('heading', { name: 'Two minutes on the beta' })).toBeVisible()
  await page.getByRole('button', { name: 'Begin' }).focus()
  await page.keyboard.press('Enter')

  await expect(page.getByRole('radio', { name: '1 of 5' })).toBeFocused()
  await page.keyboard.press('4')
  await expect(page.getByRole('radio', { name: '4 of 5' })).toBeChecked()
  await expect(page.getByRole('radio', { name: '4 of 5' })).toBeFocused()
  await page.screenshot({ path: info.outputPath('form-one-375.png'), fullPage: true, animations: 'disabled' })
  await page.keyboard.press('Enter')

  const comment = page.getByLabel('What should we fix first?')
  await expect(comment).toBeFocused()
  await page.keyboard.type('Line one')
  await page.keyboard.press('Shift+Enter')
  await page.keyboard.type('line two')
  await expect(comment).toHaveValue('Line one\nline two')
  await page.keyboard.press('Enter')

  const email = page.getByLabel(/Your email/)
  await expect(email).toBeFocused()
  await page.keyboard.press('Enter')
  await expect(page.locator('.problem')).toHaveText('This question is required.')
  await page.keyboard.type('nope')
  await page.keyboard.press('Enter')
  await expect(page.locator('.problem')).toHaveText('Enter an email address.')
  await page.keyboard.press('Escape')
  await expect(email).toHaveValue('nope')
  await email.fill('z@hanzo.ai')
  await page.keyboard.press('Enter')

  await expect(page.getByRole('checkbox', { name: 'Chat' })).toBeFocused()
  await page.keyboard.press('2')
  await page.keyboard.press('1')
  await page.keyboard.press('3')
  await page.keyboard.press('3')
  await expect(page.getByRole('checkbox', { name: 'Chat' })).toBeChecked()
  await expect(page.getByRole('checkbox', { name: 'Dev' })).toBeChecked()
  await expect(page.getByRole('checkbox', { name: 'Studio' })).not.toBeChecked()
  await expect(page.getByRole('checkbox', { name: 'Studio' })).toBeFocused()

  await page.getByRole('button', { name: 'Previous question' }).click()
  await expect(email).toHaveValue('z@hanzo.ai')
  await page.getByRole('button', { name: 'Next question' }).click()
  await expect(page.getByRole('checkbox', { name: 'Chat' })).toBeFocused()
  await page.keyboard.press('Enter')

  await expect(page.getByRole('radio', { name: 'Yes' })).toBeFocused()
  await page.keyboard.press('2')
  await expect(page.getByRole('radio', { name: 'No' })).toBeChecked()
  await expect(page.getByRole('radio', { name: 'No' })).toBeFocused()
  await expect(page.getByRole('button', { name: 'Submit' })).toBeVisible()
  await page.keyboard.press('Enter')

  await page.waitForURL(`${site}/done`)
  expect(posts).toHaveLength(1)
  expect(posts[0].answers).toEqual([
    { field: 'score', number: 4 },
    { field: 'comment', text: 'Line one\nline two' },
    { field: 'email', text: 'z@hanzo.ai' },
    { field: 'uses', choices: ['dev', 'chat'] },
    { field: 'again', boolean: false },
  ])
})

test('the widget fades only its background', async ({ page }) => {
  await open(page, sample)
  await page.goto(`${site}/?id=${sample.id}&opacity=40`)
  await expect(page.getByText('How was the beta?')).toBeVisible()
  const o = await page.evaluate(() => ({
    layer: getComputedStyle(document.querySelector('.hzform .layer') as Element).opacity,
    words: getComputedStyle(document.querySelector('.hzform .scroll') as Element).opacity,
  }))
  expect(o).toEqual({ layer: '0.4', words: '1' })
})
