import { defineConfig } from 'tsup'

export default defineConfig({
  entry: ['src/index.ts'],
  format: ['esm'],
  dts: false, // tsc emits the declarations
  clean: true,
  external: ['react', 'react-dom', 'react/jsx-runtime'],
  // Form keeps state and touches the DOM, so a Next.js server component that
  // imports it hands it to the client.
  banner: { js: "'use client'" },
  minify: false,
  sourcemap: true,
  splitting: false,
  tsconfig: 'tsconfig.json',
  outExtension() {
    return { js: '.mjs' }
  },
})
