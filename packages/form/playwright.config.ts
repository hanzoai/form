import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: 'e2e',
  fullyParallel: true,
  workers: 2,
  reporter: 'list',
  use: { ...devices['Desktop Chrome'] },
})
