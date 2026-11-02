// createWidget mounts a published form into an element of any page: the embed.

import { createElement } from 'react'
import { createRoot } from 'react-dom/client'
import { Form } from './form'
import type { Receipt } from './types'

export interface Embed {
  /** The element the form is drawn in; it fills it. */
  container: HTMLElement
  /** The values the form declares in `hidden`, by name. */
  hidden?: Record<string, string>
  hideHeaders?: boolean
  hideFooter?: boolean
  /** The background's opacity, 0 to 100 (default 100). */
  opacity?: number
  onSubmit?: (receipt: Receipt) => void
  /** The service's address; https://api.hanzo.ai when absent. */
  endpoint?: string
  /** An IAM bearer: the respondent is then the signed-in person. */
  token?: string
}

export interface Widget {
  unmount(): void
}

export function createWidget(publicId: string, options: Embed): Widget {
  const { container, opacity, ...rest } = options
  if (opacity !== undefined) {
    const o = Number.isFinite(opacity) ? Math.min(100, Math.max(0, opacity)) : 100
    container.style.setProperty('--hzform-opacity', String(o / 100))
  }
  const root = createRoot(container)
  root.render(createElement(Form, { ...rest, publicId }))
  return {
    unmount() {
      root.unmount()
      container.style.removeProperty('--hzform-opacity')
    },
  }
}
