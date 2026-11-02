// @hanzo/form: draw and answer a Hanzo form anywhere.
//
//   import { Form, createWidget, load, submit } from '@hanzo/form'
//
//   <Form publicId="hanzo.video-feedback" hidden={{ video: id }} />
//   createWidget('hanzo.video-feedback', { container: el, opacity: 80 })

export type {
  Answer,
  Choice,
  Colors,
  Drawn,
  Field,
  Labels,
  PublicForm,
  Receipt,
  Screen,
  Submission,
  Theme,
  Type,
  Validations,
} from './types'
export { load, submit, Problem } from './client'
export { check, type Read, type Value } from './check'
export { Form, type Options, type Props } from './form'
export { createWidget, type Widget, type Embed } from './widget'
