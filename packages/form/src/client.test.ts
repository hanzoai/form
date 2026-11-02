import { afterEach, describe, expect, it, vi } from 'vitest'
import { load, Problem, submit } from './client'

type Call = { url: string; init?: RequestInit }

function serve(status: number, body: unknown, type = 'application/json') {
  const calls: Call[] = []
  vi.stubGlobal('fetch', async (url: string, init?: RequestInit) => {
    calls.push({ url, init })
    const text = typeof body === 'string' ? body : JSON.stringify(body)
    return new Response(text, { status, headers: { 'content-type': type } })
  })
  return calls
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('load', () => {
  it('reads the public form from api.hanzo.ai', async () => {
    const calls = serve(200, { id: 'hanzo.video-feedback', title: 'Video feedback' })
    const form = await load('hanzo.video-feedback')
    expect(form.title).toBe('Video feedback')
    expect(calls[0].url).toBe('https://api.hanzo.ai/v1/form/public/hanzo.video-feedback')
  })

  it('takes another endpoint and escapes the id', async () => {
    const calls = serve(200, {})
    await load('acme.a/b', { endpoint: 'http://localhost:8080/' })
    expect(calls[0].url).toBe('http://localhost:8080/v1/form/public/acme.a%2Fb')
  })

  it('throws the status and the problem document detail', async () => {
    serve(404, { type: 'about:blank', title: 'Not Found', status: 404, detail: 'form: no such form' }, 'application/problem+json')
    const err = await load('hanzo.gone').catch((e: unknown) => e)
    expect(err).toBeInstanceOf(Problem)
    expect(err).toMatchObject({ status: 404, message: 'form: no such form' })
  })

  it('falls back to the title, then the status line', async () => {
    serve(429, { title: 'Too Many Requests' })
    await expect(load('hanzo.x')).rejects.toMatchObject({ status: 429, message: 'Too Many Requests' })
    serve(502, '<html>bad gateway</html>', 'text/html')
    await expect(load('hanzo.x')).rejects.toMatchObject({ status: 502 })
  })
})

describe('submit', () => {
  const body = { token: 'f_0123456789', answers: [{ field: 'score', number: 5 }], completed: true, website: '' }

  it('posts the submission as JSON and reads the receipt', async () => {
    const calls = serve(201, { id: 'r1', completed: true, submitted_at: '2026-11-02T08:35:00Z', thanks: { title: 'Thanks' } })
    const receipt = await submit('hanzo.video-feedback', body)
    expect(receipt).toEqual({ id: 'r1', completed: true, submitted_at: '2026-11-02T08:35:00Z', thanks: { title: 'Thanks' } })
    expect(calls[0].url).toBe('https://api.hanzo.ai/v1/form/public/hanzo.video-feedback/responses')
    expect(calls[0].init?.method).toBe('POST')
    expect(JSON.parse(String(calls[0].init?.body))).toEqual(body)
    const headers = calls[0].init?.headers as Record<string, string>
    expect(headers['content-type']).toBe('application/json')
    expect(headers).not.toHaveProperty('authorization')
  })

  it('sends a bearer as Authorization, never in the body', async () => {
    const calls = serve(201, { id: 'r1', completed: true })
    await submit('hanzo.video-feedback', body, { token: 'eyJ.abc.def' })
    expect((calls[0].init?.headers as Record<string, string>).authorization).toBe('Bearer eyJ.abc.def')
    expect(String(calls[0].init?.body)).not.toContain('eyJ')
  })

  it("throws a 400 with the service's words", async () => {
    serve(400, { status: 400, detail: 'form: email: is not an email address; unanswered: score' }, 'application/problem+json')
    await expect(submit('hanzo.video-feedback', body)).rejects.toMatchObject({
      status: 400,
      message: 'form: email: is not an email address; unanswered: score',
    })
  })
})
