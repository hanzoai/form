// The public half of /v1/form: read a published form, answer it. Neither needs a
// credential; a bearer, when the page has one, makes the respondent the
// signed-in person (the service reads who from the token, never from the body).

import type { PublicForm, Receipt, Submission } from './types'

/** Where the service answers unless told otherwise. */
export const endpoint = 'https://api.hanzo.ai'

/** A refusal: the HTTP status and the service's own words. */
export class Problem extends Error {
  readonly status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'Problem'
    this.status = status
  }
}

/** Reads a published form by its public id, "<org>.<slug or id>". */
export async function load(publicId: string, options: { endpoint?: string } = {}): Promise<PublicForm> {
  const res = await fetch(address(publicId, options.endpoint), { headers: { accept: 'application/json' } })
  return answer<PublicForm>(res)
}

/** Answers a published form. `token` is an IAM bearer, sent as Authorization. */
export async function submit(
  publicId: string,
  submission: Submission,
  options: { endpoint?: string; token?: string } = {},
): Promise<Receipt> {
  const headers: Record<string, string> = { accept: 'application/json', 'content-type': 'application/json' }
  if (options.token) headers.authorization = `Bearer ${options.token}`
  const res = await fetch(`${address(publicId, options.endpoint)}/responses`, {
    method: 'POST',
    headers,
    body: JSON.stringify(submission),
  })
  return answer<Receipt>(res)
}

function address(publicId: string, base = endpoint): string {
  return `${base.replace(/\/+$/, '')}/v1/form/public/${encodeURIComponent(publicId)}`
}

// answer reads a 2xx body, or throws the refusal: the problem document's detail
// (RFC 9457), else its title, else the status line.
async function answer<T>(res: Response): Promise<T> {
  if (res.ok) return (await res.json()) as T
  let message = ''
  try {
    const body = (await res.json()) as { detail?: unknown; title?: unknown }
    if (typeof body.detail === 'string' && body.detail) message = body.detail
    else if (typeof body.title === 'string' && body.title) message = body.title
  } catch {
    // A body that is not JSON says nothing the status does not.
  }
  throw new Problem(res.status, message || `${res.status} ${res.statusText}`.trim())
}
