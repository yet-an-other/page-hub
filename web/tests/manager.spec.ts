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
  await expect(page.getByRole('heading', { name: 'Your shared pages' })).toBeVisible()
  await expect(page.getByRole('link', { name: 'Page Hub' })).toBeVisible()
  await expect(page.getByText('Private manager')).toBeVisible()
  await expect(page.getByRole('button', { name: 'View exact storage usage and status' })).toBeVisible()
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

test('Publication rows show accepted facts, observed state, and private descriptions', async ({ page }) => {
  const inventory = await openInventory(page)

  // Accepted facts come from the manifest, never the observation.
  await expect(inventory.getByText('150 B', { exact: true })).toHaveCount(2)
  await expect(inventory.getByText('100 B', { exact: true })).toHaveCount(1)
  await expect(inventory.getByText('50 B', { exact: true })).toHaveCount(1)
  await expect(inventory.locator('time[title^="Content changed 2026-01-02 03:04"]')).toHaveCount(1)
  await expect(inventory.locator('time[title^="Content changed 2026-02-10 09:30"]')).toHaveCount(1)

  await expect(inventory.getByText('In sync', { exact: true })).toHaveCount(2)
  await expect(inventory.getByText('Drifted', { exact: true })).toHaveCount(1)
  await expect(inventory.getByText('Missing', { exact: true })).toHaveCount(1)

  // Descriptions render as plain text from the private catalog, directly
  // beneath each preview link and inside the Project headers.
  await expect(inventory.getByText('Adopted project notes')).toBeVisible()
  await expect(inventory.getByText('Quarterly summary deck')).toHaveCount(1)
  await expect(inventory.getByText('An empty project')).toBeVisible()

  // Staleness is stated once, for the whole observation, in the banner and
  // in the header's last-checked note.
  await expect(inventory.getByRole('status')).toContainText('Observation is stale')
  await expect(page.getByText(/Last checked .* UTC/)).toBeVisible()
})

test('selecting an observed state opens the full observation details', async ({ page }) => {
  const inventory = await openInventory(page)

  await inventory.getByRole('button', { name: 'Drifted: view observation details for Quarter Recap' }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog).toBeVisible()
  await expect(dialog).toContainText('index.html content changed')
  await expect(dialog).toContainText('150 bytes')
  await expect(dialog).toContainText('400 bytes')
  await expect(dialog).toContainText('This observation is stale')
  await page.keyboard.press('Escape')
  await expect(dialog).toBeHidden()

  await inventory.getByRole('button', { name: 'Missing: view observation details for Legacy Site' }).click()
  await expect(dialog).toContainText('no objects found under archive/legacy-site')
  await page.keyboard.press('Escape')
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
  const inventory = await openInventory(page)
  const projectOrder = await inventory.locator('h2').allTextContents()
  expect(projectOrder.map((heading) => heading.trim().split('/')[0])).toEqual(['Archive', 'Empty Shelf', 'Notes'])

  const publicationOrder = await inventory.locator('article h3').allTextContents()
  expect(publicationOrder).toEqual(['Legacy Site', 'Snapshot', '2026 Report', 'Quarter Recap'])
})

test('search covers names, prefixes, paths, entry points, and descriptions', async ({ page }) => {
  const inventory = await openInventory(page)
  const search = inventory.getByRole('searchbox', { name: 'Search Projects and Publications' })
  const count = inventory.getByText(/^\d+ (results?|Projects)$/)

  await expect(count).toHaveText('3 Projects')

  // Publication display name.
  await search.fill('quarter')
  await expect(inventory.getByText('Quarter Recap', { exact: true })).toBeVisible()
  await expect(inventory.getByText('2026 Report', { exact: true })).toBeHidden()
  await expect(count).toHaveText('1 result')

  // Project prefix matches the whole Project.
  await search.fill('archive')
  await expect(inventory.getByText('Legacy Site', { exact: true })).toBeVisible()
  await expect(inventory.getByText('Snapshot', { exact: true })).toBeVisible()
  await expect(count).toHaveText('2 results')

  // Entry point search, including case-exact uppercase.
  await search.fill('report.html')
  await expect(inventory.getByText('Snapshot', { exact: true })).toBeVisible()
  await expect(inventory.getByText('Legacy Site', { exact: true })).toBeHidden()
  await expect(count).toHaveText('1 result')

  // Private descriptions are searchable.
  await search.fill('summary deck')
  await expect(inventory.getByText('Quarter Recap', { exact: true })).toBeVisible()
  await expect(count).toHaveText('1 result')

  // Publication path search.
  await search.fill('notes/2026-report')
  await expect(inventory.getByText('2026 Report', { exact: true })).toBeVisible()
  await expect(inventory.getByText('Quarter Recap', { exact: true })).toBeHidden()

  // No matches at all, and clearing restores the unfiltered view.
  await search.fill('nothing-matches-this')
  await expect(inventory.getByText('No matching Publications')).toBeVisible()
  await expect(count).toHaveText('0 results')
  await inventory.getByRole('button', { name: 'Clear filters' }).click()
  await expect(count).toHaveText('3 Projects')
  await expect(inventory.getByText('2026 Report', { exact: true })).toBeVisible()
})

test('the state filter narrows the inventory to Publications needing attention', async ({ page }) => {
  const inventory = await openInventory(page)
  const attention = inventory.getByRole('button', { name: 'Needs attention 2' })

  await attention.click()
  await expect(inventory.getByText('Quarter Recap', { exact: true })).toBeVisible()
  await expect(inventory.getByText('Legacy Site', { exact: true })).toBeVisible()
  await expect(inventory.getByText('2026 Report', { exact: true })).toBeHidden()
  await expect(inventory.getByText('Snapshot', { exact: true })).toBeHidden()
  await expect(inventory.getByText('2 results')).toBeVisible()

  await inventory.getByRole('button', { name: 'All pages 4' }).click()
  await expect(inventory.getByText('2026 Report', { exact: true })).toBeVisible()
})

test('a search expands a collapsed Project and preserves other collapse choices', async ({ page }) => {
  const inventory = await openInventory(page)
  await projectToggle(inventory, 'Notes').click()
  await projectToggle(inventory, 'Archive').click()
  await expect(inventory.getByText('2026 Report', { exact: true })).toBeHidden()
  await expect(inventory.getByText('Legacy Site', { exact: true })).toBeHidden()

  const search = inventory.getByRole('searchbox', { name: 'Search Projects and Publications' })
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

  // "/" focuses search from anywhere outside a field.
  await inventory.getByRole('heading', { name: 'Your shared pages' }).click()
  await page.keyboard.press('/')
  await page.keyboard.type('snapshot')
  await expect(inventory.getByText('Snapshot', { exact: true })).toBeVisible()
  await expect(inventory.getByText('2026 Report', { exact: true })).toBeHidden()
})

test('the header storage control opens exact used, quota, accepted, and unclaimed usage', async ({ page }) => {
  await page.goto('/')
  const summary = page.getByRole('button', { name: 'View exact storage usage and status' })
  await expect(summary).toContainText('540 B')
  await expect(summary).toContainText('1.0 MiB')

  await summary.click()
  const dialog = page.getByRole('dialog')
  await expect(dialog).toBeVisible()
  await expect(dialog).toContainText('1,048,576 bytes')
  await expect(dialog).toContainText('540 bytes')
  await expect(dialog).toContainText('450 bytes')
  await expect(dialog).toContainText('90 bytes')
  await expect(dialog).toContainText('Stale, from the last successful scan')
  await expect(dialog).toContainText('none')
  await expect(dialog).toContainText('misconfigured')
  await page.keyboard.press('Escape')
  await expect(dialog).toBeHidden()

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
  await expect(inventory.getByText('2026 Report', { exact: true })).toBeVisible()

  // Storage is unconfigured here, so the refresh fails while every cataloged
  // value stays on screen. The mount-time refresh may still be settling, so
  // wait for the control to be enabled before clicking.
  const refreshButton = page.getByRole('button', { name: 'Refresh storage observation' })
  await expect(refreshButton).toBeEnabled({ timeout: 20_000 })
  await refreshButton.click()
  await expect(page.getByRole('alert')).toContainText('The last refresh failed')
  await expect(page.getByRole('alert')).toContainText('storage misconfigured')

  // The alert links to the exact observation values.
  await page.getByRole('alert').getByRole('button', { name: 'Details' }).click()
  await expect(page.getByRole('dialog')).toContainText('540 bytes')
  await page.keyboard.press('Escape')

  // Nothing was cleared or replaced by the failed refresh.
  await expect(inventory.getByText('2026 Report', { exact: true })).toBeVisible()
  await expect(inventory.getByText('150 B', { exact: true })).toHaveCount(2)
  await expect(inventory.getByRole('status')).toContainText('Observation is stale')

  const api = await page.request.post('/_page-hub/api/v1/refresh')
  expect(api.ok()).toBeTruthy()
  expect(await api.json()).toMatchObject({ running: false, lastOutcome: 'misconfigured' })
})

test('the Publication path opens an authenticated preview that redirects in-sync Publications', async ({ page, request }) => {
  // Request level: in-sync previews redirect to the canonical public URL.
  const redirect = await request.get('/_page-hub/preview/notes/2026-report', { maxRedirects: 0 })
  expect(redirect.status()).toBe(302)
  expect(redirect.headers().location).toBe(`${publicBase}/notes/2026-report`)

  // An exact-file page is addressed by its entry point: the URL readers
  // actually request, with its original casing.
  const exactFile = await request.get('/_page-hub/preview/archive/snapshot', { maxRedirects: 0 })
  expect(exactFile.status()).toBe(302)
  expect(exactFile.headers().location).toBe(`${publicBase}/archive/snapshot/Report.HTML`)

  const inventory = await openInventory(page)
  const context = page.context()
  const popupPromise = context.waitForEvent('page')
  await inventory.getByRole('link', { name: '/notes/2026-report', exact: true }).click()
  const popup = await popupPromise
  await popup.waitForLoadState()
  expect(new URL(popup.url()).pathname).toBe('/notes/2026-report')
  await popup.close()

  // The public-page icon opens the entry point's URL for an exact-file page.
  const snapshotPopup = context.waitForEvent('page')
  await inventory.getByRole('link', { name: 'Open Snapshot public page' }).click()
  const snapshotPage = await snapshotPopup
  await snapshotPage.waitForLoadState()
  expect(new URL(snapshotPage.url()).pathname).toBe('/archive/snapshot/Report.HTML')
  await snapshotPage.close()
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
  await inventory.getByRole('link', { name: '/notes/quarter-recap', exact: true }).click()
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
    // An exact-file page is addressed by its entry point, with its casing.
    ['Snapshot', '/archive/snapshot/Report.HTML'],
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
  // must not appear, even disabled. Interactive inventory controls are the
  // three group toggles, two filter pills, and four state badges.
  await expect(inventory.getByRole('button', { name: /^Archive/ })).toHaveCount(1)
  await expect(inventory.getByRole('button', { name: /^Empty Shelf/ })).toHaveCount(1)
  await expect(inventory.getByRole('button', { name: /^Notes/ })).toHaveCount(1)
  await expect(inventory.getByRole('button', { name: /view observation details/ })).toHaveCount(4)
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
  await inventory.getByRole('searchbox', { name: 'Search Projects and Publications' }).fill('report')

  const results = await new AxeBuilder({ page }).analyze()
  const serious = results.violations.filter(
    (violation) => violation.impact === 'serious' || violation.impact === 'critical',
  )
  expect(serious).toEqual([])
})

test('dialogs pass accessibility checks while open', async ({ page }) => {
  await page.goto('/')
  await page.getByRole('button', { name: 'View exact storage usage and status' }).click()
  const storageResults = await new AxeBuilder({ page }).analyze()
  expect(storageResults.violations.filter((violation) => violation.impact === 'serious' || violation.impact === 'critical')).toEqual([])
  await page.keyboard.press('Escape')

  const inventory = page.getByRole('region', { name: 'Publication inventory' })
  await inventory.getByRole('button', { name: 'In sync: view observation details for 2026 Report' }).click()
  const dialogResults = await new AxeBuilder({ page }).analyze()
  expect(dialogResults.violations.filter((violation) => violation.impact === 'serious' || violation.impact === 'critical')).toEqual([])
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
