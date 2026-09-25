import { expect, test } from '@playwright/test'
import AxeBuilder from '@axe-core/playwright'

const assertion = 'playwright-only-assertion'
const publicBase = 'http://127.0.0.1:4179'

async function openInventory(page: import('@playwright/test').Page) {
  await page.goto('/')
  return page.getByRole('region', { name: 'Publication inventory' })
}

function projectToggle(inventory: ReturnType<import('@playwright/test').Page['getByRole']>, name: string) {
  return inventory.getByRole('button', { name: new RegExp(`^${name}`) })
}

test('compiled binary serves the authenticated manager shell', async ({ page, request }) => {
  const unauthorized = await request.get('/', {
    headers: { 'X-Page-Hub-Assertion': '' },
  })
  expect(unauthorized.status()).toBe(401)

  const unauthenticatedInventory = await request.get('/_page-hub/api/v1/inventory', {
    headers: { 'X-Page-Hub-Assertion': '' },
  })
  expect(unauthenticatedInventory.status()).toBe(401)

  const unauthenticatedPreview = await request.get('/_page-hub/preview/notes/2026-report', {
    headers: { 'X-Page-Hub-Assertion': '' },
    maxRedirects: 0,
  })
  expect(unauthenticatedPreview.status()).toBe(401)

  const response = await page.goto('/')
  expect(response?.ok()).toBeTruthy()
  await expect(page.getByRole('heading', { name: 'Page Hub' })).toBeVisible()
  await expect(page.getByText('S3-compatible storage')).toBeVisible()
})

test('inventory shows every Project, including empty ones, and every Publication', async ({ page }) => {
  const inventory = await openInventory(page)

  for (const project of ['Archive', 'Empty Shelf', 'Notes']) {
    await expect(projectToggle(inventory, project)).toBeVisible()
  }
  for (const publication of ['2026 Report', 'Quarter Recap', 'Legacy Site', 'Snapshot']) {
    await expect(inventory.getByText(publication, { exact: true })).toBeVisible()
  }
  await expect(inventory.getByText('No Publications in this Project yet.')).toBeVisible()

  const api = await page.request.get('/_page-hub/api/v1/inventory')
  expect(api.ok()).toBeTruthy()
  const payload = await api.json()
  expect(payload.projects).toHaveLength(3)
  expect(payload.projects.map((project: { displayName: string }) => project.displayName)).toEqual([
    'Archive',
    'Empty Shelf',
    'Notes',
  ])
  const notes = payload.projects.find((project: { prefix: string }) => project.prefix === 'notes')
  expect(notes.publications).toHaveLength(2)
  expect(notes.publications[0]).toMatchObject({
    path: 'notes/2026-report',
    size: 150,
    routingMode: 'directory_index',
    canonicalUrl: `${publicBase}/notes/2026-report`,
  })
})

test('Publication rows show accepted facts, observed state, detail, time, and staleness', async ({ page }) => {
  const inventory = await openInventory(page)

  // Accepted facts come from the manifest, never the observation. The
  // Updated column collapses at narrow widths, so assert on DOM presence.
  await expect(inventory.getByRole('cell', { name: '150 B', exact: true })).toHaveCount(2)
  await expect(inventory.getByText(/Content changed 2026-01-02/)).toHaveCount(1)
  await expect(inventory.getByText(/Content changed 2026-02-10/)).toHaveCount(1)

  await expect(inventory.getByText('in sync')).toHaveCount(2)
  await expect(inventory.getByText('drifted', { exact: true })).toHaveCount(1)
  await expect(inventory.getByText('missing', { exact: true })).toHaveCount(1)

  await expect(inventory.getByText('index.html content changed')).toBeVisible()
  await expect(inventory.getByText('no objects found under archive/legacy-site')).toBeVisible()

  await expect(inventory.getByText(/^Observed 2026-01-02/)).toHaveCount(4)
  await expect(inventory.getByText('Observation is stale')).toHaveCount(4)

  // Descriptions render as plain text from the private catalog. Publication
  // descriptions live in a cell that collapses at narrow widths.
  await expect(inventory.getByText('Adopted project notes')).toBeVisible()
  await expect(inventory.getByText('Quarterly summary deck')).toHaveCount(1)
  await expect(inventory.getByText('An empty project')).toBeVisible()
})

test('Publication rows appear under collapsible Project headers', async ({ page }) => {
  const inventory = await openInventory(page)
  const notesToggle = projectToggle(inventory, 'Notes')

  await expect(notesToggle).toHaveAttribute('aria-expanded', 'true')
  await expect(inventory.getByText('2026 Report', { exact: true })).toBeVisible()

  await notesToggle.click()
  await expect(notesToggle).toHaveAttribute('aria-expanded', 'false')
  await expect(inventory.getByText('2026 Report', { exact: true })).toBeHidden()

  // Other groups keep their expansion state.
  await expect(inventory.getByText('Legacy Site', { exact: true })).toBeVisible()

  await notesToggle.click()
  await expect(inventory.getByText('2026 Report', { exact: true })).toBeVisible()
})

test('Projects and Publications sort by display name with exact path tie-breaker', async ({ page }) => {
  await openInventory(page)
  const order = await page.$$eval('tbody', (bodies) =>
    bodies.map((body) => (body.querySelector('button')?.textContent ?? '').trim().split(/\s+/)[0]),
  )
  expect(order).toEqual(['Archive', 'Empty', 'Notes'])
  // Archive publications: Legacy Site before Snapshot; Notes: 2026 Report before Quarter Recap.
  const rows = await page.$$eval('tbody', (bodies) =>
    bodies.map((body) => [...body.querySelectorAll('tr')].slice(1).map((row) => row.textContent ?? '')),
  )
  expect(rows[0][0]).toContain('Legacy Site')
  expect(rows[0][1]).toContain('Snapshot')
  expect(rows[2][0]).toContain('2026 Report')
  expect(rows[2][1]).toContain('Quarter Recap')
})

test('search covers names, prefixes, paths, entry points, and descriptions', async ({ page }) => {
  const inventory = await openInventory(page)
  const search = inventory.getByRole('searchbox', { name: 'Search projects and publications' })
  const count = inventory.getByRole('status')

  // Publication display name.
  await search.fill('quarter')
  await expect(inventory.getByText('Quarter Recap', { exact: true })).toBeVisible()
  await expect(inventory.getByText('2026 Report', { exact: true })).toBeHidden()
  await expect(count).toHaveText(/1 project · 1 publication/)

  // Project prefix matches the whole Project.
  await search.fill('archive')
  await expect(inventory.getByText('Legacy Site', { exact: true })).toBeVisible()
  await expect(inventory.getByText('Snapshot', { exact: true })).toBeVisible()
  await expect(count).toHaveText(/1 project · 2 publications/)

  // Entry point search, including case-exact uppercase.
  await search.fill('report.html')
  await expect(inventory.getByText('Snapshot', { exact: true })).toBeVisible()
  await expect(inventory.getByText('Legacy Site', { exact: true })).toBeHidden()
  await expect(count).toHaveText(/1 project · 1 publication/)

  // Private descriptions are searchable.
  await search.fill('summary deck')
  await expect(inventory.getByText('Quarter Recap', { exact: true })).toBeVisible()
  await expect(count).toHaveText(/1 project · 1 publication/)

  // Publication path search.
  await search.fill('notes/2026-report')
  await expect(inventory.getByText('2026 Report', { exact: true })).toBeVisible()
  await expect(inventory.getByText('Quarter Recap', { exact: true })).toBeHidden()

  // No matches at all.
  await search.fill('nothing-matches-this')
  await expect(inventory.getByText('No Projects or Publications match this search.')).toBeVisible()
  await expect(count).toHaveText(/0 projects · 0 publications/)
})

test('a search expands a collapsed Project and preserves other collapse choices', async ({ page }) => {
  const inventory = await openInventory(page)
  await projectToggle(inventory, 'Notes').click()
  await projectToggle(inventory, 'Archive').click()
  await expect(inventory.getByText('2026 Report', { exact: true })).toBeHidden()
  await expect(inventory.getByText('Legacy Site', { exact: true })).toBeHidden()

  const search = inventory.getByRole('searchbox', { name: 'Search projects and publications' })
  await search.fill('quarter')
  // The matching group expands despite its collapse choice.
  await expect(inventory.getByText('Quarter Recap', { exact: true })).toBeVisible()

  // The other collapsed group is not discarded: its rows stay hidden while
  // the search is active (it has no match in this search).
  await expect(inventory.getByText('Legacy Site', { exact: true })).toBeHidden()

  // Clearing the search restores every collapse choice.
  await search.fill('')
  await expect(inventory.getByText('2026 Report', { exact: true })).toBeHidden()
  await expect(inventory.getByText('Legacy Site', { exact: true })).toBeHidden()

  await projectToggle(inventory, 'Notes').click()
  await expect(inventory.getByText('2026 Report', { exact: true })).toBeVisible()
  await expect(inventory.getByText('Legacy Site', { exact: true })).toBeHidden()
})

test('keyboard operation toggles groups and drives search', async ({ page }) => {
  const inventory = await openInventory(page)
  const notesToggle = projectToggle(inventory, 'Notes')
  await notesToggle.focus()
  await page.keyboard.press('Enter')
  await expect(notesToggle).toHaveAttribute('aria-expanded', 'false')
  await expect(inventory.getByText('2026 Report', { exact: true })).toBeHidden()
  await page.keyboard.press('Space')
  await expect(notesToggle).toHaveAttribute('aria-expanded', 'true')
  await expect(inventory.getByText('2026 Report', { exact: true })).toBeVisible()

  const search = inventory.getByRole('searchbox', { name: 'Search projects and publications' })
  await search.focus()
  await page.keyboard.type('snapshot')
  await expect(inventory.getByText('Snapshot', { exact: true })).toBeVisible()
  await expect(inventory.getByText('2026 Report', { exact: true })).toBeHidden()
})

test('the inventory shows exact used, quota, accepted, and unclaimed usage', async ({ page }) => {
  await page.goto('/')
  const usage = page.getByRole('region', { name: 'Storage usage' })
  await expect(usage.getByText('Quota')).toBeVisible()
  await expect(usage.getByText('1.0 MiB')).toBeVisible()
  await expect(usage.getByText('540 B')).toBeVisible()
  await expect(usage.getByText('450 B')).toBeVisible()
  await expect(usage.getByText('90 B')).toBeVisible()
  await expect(usage.getByText(/This observation is stale/)).toBeVisible()

  const api = await page.request.get('/_page-hub/api/v1/inventory')
  const payload = await api.json()
  expect(payload.observation).toMatchObject({
    stale: true,
    mutationLock: 'none',
    usage: { quotaBytes: 1048576, totalBytes: 540, acceptedBytes: 450, unclaimedBytes: 90 },
  })
})

test('stale inventory stays readable through a failed refresh', async ({ page }) => {
  await page.goto('/')
  const inventory = page.getByRole('region', { name: 'Publication inventory' })
  const usage = page.getByRole('region', { name: 'Storage usage' })
  await expect(inventory.getByText('2026 Report', { exact: true })).toBeVisible()

  // Storage is unconfigured here, so the refresh fails while every cataloged
  // value stays on screen. The mount-time refresh may still be settling, so
  // wait for the control to be enabled before clicking.
  const refreshButton = page.getByRole('button', { name: 'Refresh' })
  await expect(refreshButton).toBeEnabled({ timeout: 20_000 })
  await refreshButton.click()
  await expect(page.getByRole('alert')).toContainText('The last refresh failed')
  await expect(page.getByRole('alert')).toContainText('storage misconfigured')

  // Nothing was cleared or replaced by the failed refresh.
  await expect(inventory.getByText('2026 Report', { exact: true })).toBeVisible()
  await expect(inventory.getByRole('cell', { name: '150 B', exact: true })).toHaveCount(2)
  await expect(usage.getByText('1.0 MiB')).toBeVisible()
  await expect(usage.getByText(/This observation is stale/)).toBeVisible()

  const api = await page.request.post('/_page-hub/api/v1/refresh')
  expect(api.ok()).toBeTruthy()
  expect(await api.json()).toMatchObject({ running: false, lastOutcome: 'misconfigured' })
})

test('the Publication path opens an authenticated preview that redirects in-sync Publications', async ({ page, request }) => {
  // Request level: in-sync previews redirect to the canonical public URL.
  const redirect = await request.get('/_page-hub/preview/notes/2026-report', { maxRedirects: 0 })
  expect(redirect.status()).toBe(302)
  expect(redirect.headers().location).toBe(`${publicBase}/notes/2026-report`)

  const inventory = await openInventory(page)
  const context = page.context()
  const popupPromise = context.waitForEvent('page')
  await inventory.getByRole('link', { name: '/notes/2026-report/' }).click()
  const popup = await popupPromise
  await popup.waitForLoadState()
  expect(new URL(popup.url()).pathname).toBe('/notes/2026-report')
  await popup.close()
})

test('drifted and missing Publications receive a warning page instead of a redirect', async ({ page, request }) => {
  for (const [path, fragment] of [
    ['notes/quarter-recap', 'drifted'],
    ['archive/legacy-site', 'missing'],
  ] as const) {
    const response = await request.get(`/_page-hub/preview/${path}`, { maxRedirects: 0 })
    expect(response.status()).toBe(409)
    expect(response.headers().location).toBeUndefined()
    expect(await response.text()).toContain(fragment)
  }

  const inventory = await openInventory(page)
  const context = page.context()
  const popupPromise = context.waitForEvent('page')
  await inventory.getByRole('link', { name: '/notes/quarter-recap/' }).click()
  const popup = await popupPromise
  await expect(popup.getByRole('heading', { name: 'Preview unavailable' })).toBeVisible()
  await expect(popup.getByText(/drifted from its accepted manifest/)).toBeVisible()
  await expect(popup.getByText('index.html content changed')).toBeVisible()
  await expect(popup.getByText(/This observation is stale/)).toBeVisible()
  // The direct public link stays available from the warning page.
  await expect(popup.getByRole('link', { name: 'Open the public page anyway' })).toHaveAttribute(
    'href',
    `${publicBase}/notes/quarter-recap`,
  )
  await popup.close()
})

test('the public-page icon stays available in every state and opens the canonical URL', async ({ page }) => {
  const inventory = await openInventory(page)
  const icons = inventory.getByRole('link', { name: /Open .* public page/ })
  await expect(icons).toHaveCount(4)

  for (const [name, canonical] of [
    ['2026 Report', '/notes/2026-report'],
    ['Quarter Recap', '/notes/quarter-recap'],
    ['Legacy Site', '/archive/legacy-site'],
    ['Snapshot', '/archive/snapshot'],
  ] as const) {
    const icon = inventory.getByRole('link', { name: `Open ${name} public page` })
    await expect(icon).toHaveAttribute('target', '_blank')
    await expect(icon).toHaveAttribute('rel', /noopener/)
    await expect(icon).toHaveAttribute('rel', /noreferrer/)
    await expect(icon).toHaveAttribute('href', `${publicBase}${canonical}`)
  }
})

test('the manager never embeds public Publication content and shows no deferred controls', async ({ page }) => {
  const inventory = await openInventory(page)
  const frames = await page.locator('iframe, frame, embed, object').count()
  expect(frames).toBe(0)

  // Description editing, move, deletion, and reconciliation are deferred and
  // must not appear, even disabled.
  await expect(inventory.getByRole('button')).toHaveCount(3) // three group toggles
  const managerText = await inventory.innerText()
  expect(managerText).not.toMatch(/edit description|move|delete|reconcile/i)
})

test('manager assets stay below the reserved prefix', async ({ page }) => {
  const response = await page.goto('/')
  expect(response?.ok()).toBeTruthy()
  const assetURLs = await page.locator('script[src], link[rel="stylesheet"]').evaluateAll((elements) =>
    elements.map((element) => (element as HTMLScriptElement | HTMLLinkElement).src || (element as HTMLLinkElement).href),
  )
  expect(assetURLs.length).toBeGreaterThan(0)
  for (const url of assetURLs) expect(new URL(url).pathname).toMatch(/^\/_page-hub\//)
})

test('the inventory passes accessibility checks with search and collapse active', async ({ page }) => {
  const inventory = await openInventory(page)
  await projectToggle(inventory, 'Notes').click()
  await inventory.getByRole('searchbox', { name: 'Search projects and publications' }).fill('report')

  const results = await new AxeBuilder({ page }).analyze()
  const serious = results.violations.filter(
    (violation) => violation.impact === 'serious' || violation.impact === 'critical',
  )
  expect(serious).toEqual([])
})

test('the preview warning page passes accessibility checks', async ({ page, request }) => {
  const response = await request.get('/_page-hub/preview/archive/legacy-site', { maxRedirects: 0 })
  expect(response.status()).toBe(409)

  await page.goto('/_page-hub/preview/archive/legacy-site')
  const results = await new AxeBuilder({ page }).analyze()
  const serious = results.violations.filter(
    (violation) => violation.impact === 'serious' || violation.impact === 'critical',
  )
  expect(serious).toEqual([])
})
