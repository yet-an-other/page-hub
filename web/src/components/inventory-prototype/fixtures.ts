// THROWAWAY. Synthetic public-safe data based on the first UI prototype, not the catalog.
import type { components } from '../../api/generated'

export type InventoryData = components['schemas']['Inventory']
export type Project = components['schemas']['InventoryProject']
export type Publication = components['schemas']['InventoryPublication']
export type Observation = components['schemas']['BucketObservation']
export type Scenario = 'mixed' | 'synced' | 'stale' | 'failed' | 'unobserved'

function publication(id: string, displayName: string, path: string, description: string, size: number, day: number, entryPoint = 'index.html', state: 'in_sync' | 'drifted' | 'missing' = 'in_sync'): Publication {
  return {
    id, displayName, path, description, size, entryPoint,
    routingMode: entryPoint === 'index.html' ? 'directory_index' : 'exact_file',
    canonicalUrl: `https://example.invalid/${path}/${entryPoint === 'index.html' ? '' : entryPoint}`,
    contentChangedAt: `2026-09-${day}T09:24:00Z`,
    observation: {
      state,
      observedSize: state === 'missing' ? null : size + (state === 'drifted' ? 2048 : 0),
      statusDetail: state === 'drifted' ? 'The stored HTML differs from the accepted manifest. The accepted content has not changed.' : state === 'missing' ? 'The entry point was not found in storage. The Publication remains in the catalog.' : '',
    },
  }
}

const projects: Project[] = [
  { id: 'page-hub', prefix: 'page-hub', displayName: 'Page Hub', description: 'Research and prototypes for the publication manager.', publications: [
    publication('api', 'API review', 'page-hub/research', 'Review copy of the publishing API proposal, including replacement semantics, validation failures, and recovery after interrupted uploads.', 14361, 24, 'api-review.html'),
    publication('launch', 'Launch notes', 'page-hub/releases/launch-notes', 'Release notes for the first rollout. Includes the migration checklist and the decisions we want to revisit after a week of use.', 42586, 24),
    publication('layout', 'Layout study', 'page-hub/prototypes/layout-study', 'The original inventory layout, kept as a reference for spacing, grouping, and quiet row actions.', 16000, 22, 'index.html', 'drifted'),
    publication('reader', 'Reader prototype', 'page-hub/prototypes/reader', 'A small experiment in document navigation. Superseded by the grouped inventory, but useful for comparing how long descriptions read.', 26698, 20),
  ] },
  { id: 'platform', prefix: 'platform-notes', displayName: 'Platform notes', description: 'Shared infrastructure, storage, and service operation.', publications: [
    publication('auth', 'Auth comparison', 'platform-notes/auth', 'Comparison of manager sign-in options and the boundary between private management routes and public pages.', 41177, 23, 'notes.html'),
    publication('decision', 'Decision log', 'platform-notes/decisions', 'Rendered architecture decisions for quick sharing. The source stays in the repository; this copy is for review.', 20000, 22),
    publication('migration', 'Migration report', 'platform-notes/migration', 'Findings from the storage migration rehearsal. Check the named entry point and case-sensitive paths before accepting a replacement.', 48780, 21, 'migration-report.html', 'missing'),
    publication('storage', 'Storage findings and routing compatibility', 'platform-notes/research/storage-routing-compatibility', 'Notes on quota, object layout, and fallback routing. This longer description deliberately uses the available width instead of squeezing into a narrow column beside repeated observation timestamps.', 27549, 20, 'StorageFindings.html'),
  ] },
  { id: 'workshop', prefix: 'workshop-kit', displayName: 'Workshop kit', description: 'Pages for workshops and product reviews.', publications: [
    publication('brief', 'Quarterly brief', 'workshop-kit/quarterly', 'Review brief with a case-sensitive entry point. Keep the capital letters in the public URL.', 56586, 19, 'QuarterlyBrief.html'),
    publication('board', 'Workshop board', 'workshop-kit/september', '', 18000, 18),
  ] },
  { id: 'archive', prefix: 'working-drafts', displayName: 'Working drafts', description: 'An empty Project, ready for its first Publication.', publications: [] },
]

export function sampleInventory(scenario: Scenario = 'mixed'): InventoryData {
  const copy = structuredClone(projects)
  const all = copy.flatMap(project => project.publications)
  if (scenario === 'synced') all.forEach(publication => {
    publication.observation = { state: 'in_sync', observedSize: publication.size, statusDetail: '' }
  })
  if (scenario === 'unobserved') all.forEach(publication => { publication.observation = null })
  const acceptedBytes = all.reduce((sum, publication) => sum + publication.size, 0)
  const unclaimedBytes = scenario === 'synced' ? 0 : 8192
  return {
    projects: copy,
    observation: scenario === 'unobserved' ? null : {
      observedAt: scenario === 'stale' || scenario === 'failed' ? '2026-09-25T08:42:00Z' : '2026-09-25T10:42:00Z',
      stale: scenario === 'stale' || scenario === 'failed',
      mutationLock: scenario === 'synced' ? 'none' : 'global',
      usage: {
        acceptedBytes, unclaimedBytes, quotaBytes: 30 * 1024 ** 3,
        totalBytes: all.reduce((sum, publication) => sum + (publication.observation?.observedSize ?? 0), 0) + unclaimedBytes,
      },
    },
    refresh: { running: false, lastOutcome: scenario === 'failed' ? 'unavailable' : scenario === 'unobserved' ? 'never' : 'succeeded' },
  }
}

export const sampleStatus: components['schemas']['ManagerStatus'] = { version: '0.6.1', storage: { status: 'reachable' } }
