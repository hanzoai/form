// The page the specs draw: the embed, as any site would call it. The form's
// public id comes from the address (?id=), and the receipt is left on window
// for the spec to read.

import { createWidget, type Receipt } from '../src/index'

declare global {
  interface Window {
    receipt?: Receipt
  }
}

const q = new URLSearchParams(location.search)
const container = document.getElementById('root')
if (!container) throw new Error('page: no #root')
createWidget(q.get('id') ?? 'hanzo.sample', {
  container,
  hidden: { video: '7mVTEAHHjPc', plan: 'pro' },
  opacity: Number(q.get('opacity') ?? 100),
  onSubmit: (r) => {
    window.receipt = r
  },
})
