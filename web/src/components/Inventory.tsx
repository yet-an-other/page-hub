import { useEffect, useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'

import type { components } from '../api/generated'
import { formatBytes, formatExactBytes, formatDate, formatShortDate, formatShortDateTime } from '../lib/format'
import { ChevronIcon, ExternalIcon, FolderIcon, InfoIcon, LockIcon, SearchIcon } from './icons'
import { Dialog } from './ui/dialog'

type InventoryProject = components['schemas']['InventoryProject']
type InventoryPublication = components['schemas']['InventoryPublication']
type BucketObservation = components['schemas']['BucketObservation']
type RefreshState = components['schemas']['RefreshState']

const observedStyles = {
  in_sync: 'bg-[#eff5ee] text-[#426e51]',
  drifted: 'bg-[#fbf2df] text-[#93641d]',
  missing: 'bg-[#fbeeea] text-[#a25249]',
} as const

const observedLabels = {
  in_sync: 'In sync',
  drifted: 'Drifted',
  missing: 'Missing',
} as const

const banner =
  'mb-5 flex flex-wrap items-center gap-x-3 gap-y-1 rounded-md border border-[#e6dfce] bg-[#faf6ed] px-3.5 py-2.5 text-[11px] text-[#785e30]'
const bannerAction = 'whitespace-nowrap font-medium underline-offset-2 hover:underline'
const pill = (active: boolean) =>
  `flex items-center gap-1.5 whitespace-nowrap rounded-md px-2 py-1.5 text-[11px] transition-colors ${
    active ? 'border border-[#d7e0d3] bg-[#eaf0e6] text-[#385b44]' : 'border border-transparent text-muted hover:bg-[#edf1e8]'
  }`
const iconButton =
  'inline-grid h-[30px] w-[30px] flex-none place-items-center rounded-md border border-line bg-white p-0 text-[#68786c] hover:border-[#a5b7a9] hover:bg-[#edf5ef] hover:text-moss'

// previewHref addresses the authenticated preview route for one Publication
function previewHref(publicationPath: string): string {
  return `/_page-hub/preview/${publicationPath.split('/').map(encodeURIComponent).join('/')}`
}

// publicPathText renders the canonical public path of a Publication from
// the API's canonical URL, so the text always names the destination the
// public-page icon and an in-sync preview redirect open.
function publicPathText(publication: InventoryPublication): string {
  if (publication.canonicalUrl) {
    try {
      return new URL(publication.canonicalUrl).pathname
    } catch {
      // fall through to the managed path
    }
  }
  return `/${publication.path}`
}

function matchesQuery(query: string, values: Array<string | undefined>): boolean {
  return values.some((value) => value?.toLowerCase().includes(query))
}

// StateBadge keeps the observed state on one line. The full status detail,
// observation time, and staleness open in the Publication details dialog.
function StateBadge({ publication, onInspect }: { publication: InventoryPublication; onInspect: (publication: InventoryPublication) => void }) {
  const state = publication.observation?.state
  const label = state ? observedLabels[state] : 'Not checked'
  return (
    <button
      type="button"
      onClick={() => onInspect(publication)}
      title="View observation details"
      aria-label={`${label}: view observation details for ${publication.displayName}`}
      className={`inline-flex w-max items-center gap-1.5 whitespace-nowrap rounded-md px-[7px] py-[3px] text-[10px] font-medium leading-none hover:ring-1 hover:ring-current ${
        state ? observedStyles[state] : 'bg-[#edf0ec] text-[#667267]'
      }`}
    >
      <span aria-hidden="true" className="h-[5px] w-[5px] rounded-full bg-current" />
      {label}
      <InfoIcon className="h-3 w-3 opacity-60" />
    </button>
  )
}

function Description({ publication }: { publication: InventoryPublication }) {
  return (
    <p className={`min-w-0 break-words text-[13px] leading-relaxed text-[#5c695f] ${publication.description ? '' : 'text-[11px] italic text-[#647064]'}`}>
      {publication.description || 'No private description'}
    </p>
  )
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 border-b border-line py-2.5 text-[12px]">
      <dt className="text-muted">{label}</dt>
      <dd className="m-0 break-all text-right">{children}</dd>
    </div>
  )
}

function PublicationDetails({ publication, observation, onClose }: { publication: InventoryPublication; observation?: BucketObservation; onClose: () => void }) {
  const state = publication.observation?.state
  return (
    <Dialog title={publication.displayName} onClose={onClose}>
      <span
        className={`inline-flex items-center gap-1.5 rounded-md px-[7px] py-[3px] text-[10px] font-medium leading-none ${
          state ? observedStyles[state] : 'bg-[#edf0ec] text-[#667267]'
        }`}
      >
        <span aria-hidden="true" className="h-[5px] w-[5px] rounded-full bg-current" />
        {state ? observedLabels[state] : 'Not checked'}
      </span>
      <p className="mb-4 mt-3 text-[12px] leading-relaxed text-muted">
        {publication.observation?.statusDetail ||
          (state ? 'Stored objects match the accepted manifest.' : 'No successful storage observation yet.')}
      </p>
      {observation?.stale && (
        <p className="rounded-md bg-[#faf4e8] px-3 py-2.5 text-[12px] text-[#7c622e]">
          This observation is stale. These are the values from the last successful scan.
        </p>
      )}
      <dl className="my-4">
        <Fact label="Public path">
          <code className="font-mono text-[11px]">{publicPathText(publication)}</code>
        </Fact>
        <Fact label="Entry point">
          <code className="font-mono text-[11px]">{publication.entryPoint}</code>
        </Fact>
        <Fact label="Routing">{publication.routingMode.replace('_', ' ')}</Fact>
        <Fact label="Accepted size">{formatExactBytes(publication.size)}</Fact>
        <Fact label="Observed size">
          {publication.observation?.observedSize == null ? 'Unavailable' : formatExactBytes(publication.observation.observedSize)}
        </Fact>
        <Fact label="Content changed">{formatDate(publication.contentChangedAt)}</Fact>
        <Fact label="Observed">{observation ? formatDate(observation.observedAt) : 'Not yet'}</Fact>
      </dl>
      <p className="text-[10px] font-semibold uppercase tracking-wide text-muted">Private description</p>
      <Description publication={publication} />
    </Dialog>
  )
}

// The row is a five-column grid on desktop and reflows into stacked
// fragments below lg. Reflow — never horizontal scrolling — keeps the
// description, state, and metadata readable at narrow widths.
const rowClasses =
  'grid grid-cols-[minmax(0,1fr)_auto_auto_30px] items-start gap-x-4 gap-y-2 border-b border-[#edf0eb] px-4 py-4 transition-colors last:border-b-0 hover:bg-[#fcfdfb] lg:grid-cols-[minmax(0,1fr)_98px_108px_73px_30px] lg:gap-5 lg:px-5 lg:py-[17px]'
const copyPlacement = 'col-start-1 col-span-3 row-start-1 lg:col-auto lg:row-start-auto'
const descriptionPlacement = 'min-w-0 col-span-full row-start-2 lg:col-auto lg:row-start-auto'
const statePlacement = 'col-start-1 row-start-3 lg:col-auto lg:row-start-auto'
const datePlacement = 'col-start-2 row-start-3 whitespace-nowrap text-[11px] tabular-nums text-muted lg:col-auto lg:row-start-auto'
const sizePlacement = 'col-start-3 col-span-2 row-start-3 whitespace-nowrap text-right font-mono text-[10px] tabular-nums text-muted lg:col-auto lg:row-start-auto'
const iconPlacement = 'col-start-4 row-start-1 lg:col-auto lg:row-start-auto'

function PublicationRow({ publication, observation, onInspect }: { publication: InventoryPublication; observation?: BucketObservation; onInspect: (publication: InventoryPublication) => void }) {
  return (
    <article className={rowClasses}>
      {/* Below lg the copy wrapper dissolves so the description can span the
          full row width beneath the one-line preview link. */}
      <div className="contents lg:block lg:min-w-0">
        <div className={copyPlacement}>
          <h3 className="break-words text-[13px] font-semibold leading-normal">{publication.displayName}</h3>
          <a
            href={previewHref(publication.path)}
            target="_blank"
            rel="noopener"
            title={`Status-checked preview in Page Hub: ${publicPathText(publication)}`}
            className="mt-1 block truncate font-mono text-[10px] text-[#637366] underline decoration-[#ced8ce] underline-offset-[3px] hover:text-moss hover:decoration-moss"
          >
            {publicPathText(publication)}
          </a>
        </div>
        <div className={descriptionPlacement}>
          <Description publication={publication} />
        </div>
      </div>
      <div className={statePlacement}>
        <StateBadge publication={publication} onInspect={onInspect} />
      </div>
      <time dateTime={publication.contentChangedAt} title={`Content changed ${formatDate(publication.contentChangedAt)}`} className={datePlacement}>
        <span className="lg:hidden">Content changed </span>
        {formatShortDate(publication.contentChangedAt)}
      </time>
      <span title={`Accepted size: ${formatExactBytes(publication.size)}`} className={sizePlacement}>
        {formatBytes(publication.size)}
      </span>
      <a
        href={publication.canonicalUrl}
        target="_blank"
        rel="noopener noreferrer"
        aria-label={`Open ${publication.displayName} public page`}
        title="Open public page"
        className={`${iconButton} ${iconPlacement}`}
      >
        <ExternalIcon className="h-4 w-4" />
      </a>
    </article>
  )
}

function ProjectGroup({
  project,
  publications,
  collapsed,
  onToggle,
  observation,
  onInspect,
}: {
  project: InventoryProject
  publications: InventoryPublication[]
  collapsed: boolean
  onToggle: () => void
  observation?: BucketObservation
  onInspect: (publication: InventoryPublication) => void
}) {
  const count = publications.length
  return (
    <section aria-label={project.displayName}>
      <header className="flex items-center gap-4 border-y border-line bg-[#f1f4ef] px-4 py-3 lg:px-5">
        <h2 className="flex min-w-0 flex-1">
          <button
            type="button"
            onClick={onToggle}
            aria-expanded={!collapsed}
            aria-controls={`publications-${project.id}`}
            className="group flex min-w-0 flex-1 items-center gap-2 text-left"
          >
            <ChevronIcon className={`h-3 w-3 flex-none text-[#7d897e] transition-transform ${collapsed ? '' : 'rotate-90'}`} />
            <FolderIcon className="hidden h-[17px] w-[17px] flex-none text-[#788b78] sm:block" />
            <span className="min-w-0">
              <span className="flex flex-wrap items-baseline gap-x-3 text-[13px] font-semibold group-hover:text-moss">
                {project.displayName}
                <code className="break-all font-mono text-[10px] font-normal text-[#647365]">/{project.prefix}/</code>
              </span>
              {project.description && <span className="mt-0.5 block break-words text-[11px] text-muted">{project.description}</span>}
            </span>
          </button>
        </h2>
        <span className="whitespace-nowrap text-[10px] text-muted">
          {count} publication{count === 1 ? '' : 's'}
        </span>
      </header>
      <div id={`publications-${project.id}`} hidden={collapsed}>
        {count === 0 ? (
          <p className="px-4 py-6 text-[12px] text-muted lg:px-10">No Publications in this Project yet.</p>
        ) : (
          publications.map((publication) => (
            <PublicationRow key={publication.id} publication={publication} observation={observation} onInspect={onInspect} />
          ))
        )}
      </div>
    </section>
  )
}

const outcomeLabels: Record<string, string> = {
  unavailable: 'storage unavailable',
  misconfigured: 'storage misconfigured',
  failed: 'the scan failed',
}

function refreshWarning(refresh: RefreshState): string | null {
  if (refresh.running) return null
  const reason = outcomeLabels[refresh.lastOutcome]
  if (reason) {
    return `The last refresh failed (${reason}). The values shown come from the last successful scan.`
  }
  return null
}

export function Inventory({
  projects,
  observation,
  refresh,
  refreshPending,
  version,
  onOpenStorage,
}: {
  projects: InventoryProject[]
  observation?: BucketObservation
  refresh: RefreshState
  refreshPending: boolean
  version: string
  onOpenStorage: () => void
}) {
  const [searchText, setSearchText] = useState('')
  // Collapsed Projects stay collapsed across searches: a search expands
  // matching groups visually without discarding the operator's choices.
  const [collapsedIds, setCollapsedIds] = useState<ReadonlySet<string>>(new Set())
  const [filter, setFilter] = useState<'all' | 'attention'>('all')
  const [selected, setSelected] = useState<InventoryPublication | null>(null)
  const searchRef = useRef<HTMLInputElement>(null)

  const query = searchText.trim().toLowerCase()
  const everyPublication = projects.flatMap((project) => project.publications)
  const attentionCount = everyPublication.filter((publication) => publication.observation && publication.observation.state !== 'in_sync').length
  const filtering = query !== '' || filter !== 'all'

  const groups = useMemo(() => {
    return projects.flatMap((project) => {
      const projectMatches = matchesQuery(query, [project.displayName, project.prefix, project.description])
      const publications = project.publications.filter(
        (publication) =>
          (projectMatches || matchesQuery(query, [publication.displayName, publication.path, publication.entryPoint, publication.description])) &&
          (filter === 'all' || (publication.observation && publication.observation.state !== 'in_sync')),
      )
      const includeEmptyProject = projectMatches && filter === 'all' && project.publications.length === 0
      return publications.length || includeEmptyProject ? [{ project, publications }] : []
    })
  }, [projects, query, filter])

  const shownCount = groups.reduce((sum, group) => sum + group.publications.length, 0)
  const toggle = (projectId: string) => {
    setCollapsedIds((current) => {
      const next = new Set(current)
      if (next.has(projectId)) {
        next.delete(projectId)
      } else {
        next.add(projectId)
      }
      return next
    })
  }

  // "/" focuses search from anywhere outside a field or dialog.
  useEffect(() => {
    const keydown = (event: KeyboardEvent) => {
      if (event.key !== '/' || document.querySelector('dialog[open]')) return
      if ((event.target as HTMLElement).closest('input, textarea, select, [contenteditable]:not([contenteditable="false"])')) return
      event.preventDefault()
      searchRef.current?.focus()
    }
    window.addEventListener('keydown', keydown)
    return () => window.removeEventListener('keydown', keydown)
  }, [])

  const warning = refresh.running || refreshPending ? null : refreshWarning(refresh)
  // Staleness is a property of the stored observation, so it is reported
  // even when a later refresh attempt failed for another reason.
  const showStaleBanner = Boolean(observation?.stale)
  const showFindingsBanner = observation && !showStaleBanner && observation.mutationLock !== 'none'

  return (
    <section aria-label="Publication inventory" className="flex flex-col">
      <div className="mb-6 flex flex-wrap items-end justify-between gap-x-5 gap-y-3">
        <div>
          <p className="mb-1.5 text-[10px] font-bold uppercase tracking-[0.12em] text-moss">Project inventory</p>
          <h1 className="flex items-center gap-3.5 text-[28px] font-semibold leading-tight tracking-tight sm:text-[32px]">
            Your shared pages
            <span className="inline-grid h-[26px] min-w-[29px] place-items-center rounded-md border border-[#d9e1d9] bg-[#eef2eb] px-1.5 text-[13px] font-medium tracking-normal text-muted">
              {everyPublication.length}
            </span>
          </h1>
          <p className="mt-2 text-[12px] text-muted">Public pages, privately organized.</p>
        </div>
        <p className="hidden items-center gap-1.5 pb-1 text-[11px] text-muted sm:flex">
          <LockIcon className="h-3.5 w-3.5" />
          Descriptions are private
        </p>
      </div>

      {warning && (
        <p role="alert" className={banner}>
          <span className="min-w-0 flex-1">{warning}</span>
          <button type="button" onClick={onOpenStorage} className={bannerAction}>
            Details
          </button>
        </p>
      )}
      {showStaleBanner && (
        <p role="status" className={banner}>
          <span className="min-w-0 flex-1">Observation is stale. Publication states are from the last successful scan.</span>
          <button type="button" onClick={onOpenStorage} className={bannerAction}>
            View observation
          </button>
        </p>
      )}
      {showFindingsBanner && observation && (
        <p role="status" className={banner}>
          <span className="min-w-0 flex-1">
            {attentionCount} Publication{attentionCount === 1 ? '' : 's'} need{attentionCount === 1 ? 's' : ''} attention ·{' '}
            {formatBytes(observation.usage.unclaimedBytes)} unclaimed · storage writes locked
          </span>
          <button type="button" onClick={onOpenStorage} className={bannerAction}>
            View observation
          </button>
        </p>
      )}

      <div className="mb-4 flex flex-wrap items-center gap-x-5 gap-y-3">
        <label className="flex h-[38px] w-full max-w-sm flex-1 items-center gap-2 rounded-lg border border-[#d9e0d8] bg-white px-3 focus-within:border-moss">
          <SearchIcon className="h-[17px] w-[17px] flex-none text-muted" />
          <input
            ref={searchRef}
            type="search"
            value={searchText}
            onChange={(event) => setSearchText(event.target.value)}
            placeholder="Search Projects, paths, descriptions…"
            aria-label="Search Projects and Publications"
            className="min-w-0 flex-1 bg-transparent text-[12px] outline-none placeholder:text-[#778179]"
          />
          <kbd className="hidden rounded border border-[#e6e9e4] px-1 font-mono text-[11px] text-muted sm:block">/</kbd>
        </label>
        <div role="group" aria-label="Publication filters" className="flex gap-1">
          <button type="button" aria-pressed={filter === 'all'} onClick={() => setFilter('all')} className={pill(filter === 'all')}>
            All pages <span>{everyPublication.length}</span>
          </button>
          <button
            type="button"
            aria-pressed={filter === 'attention'}
            onClick={() => setFilter('attention')}
            className={pill(filter === 'attention')}
          >
            Needs attention <span>{attentionCount}</span>
          </button>
        </div>
        <p aria-live="polite" className="whitespace-nowrap text-[11px] text-muted sm:ml-auto">
          {filtering
            ? `${shownCount} result${shownCount === 1 ? '' : 's'}`
            : `${groups.length} Project${groups.length === 1 ? '' : 's'}`}
        </p>
      </div>

      {groups.length === 0 ? (
        <div className="flex flex-col items-center gap-3 rounded-[10px] border border-line bg-white px-5 py-16 text-center text-muted">
          <SearchIcon className="h-6 w-6" />
          <p className="text-[15px] font-medium text-ink">No matching Publications</p>
          <p className="text-[12px]">Try a name, path, entry point, or private description.</p>
          <button
            type="button"
            onClick={() => {
              setSearchText('')
              setFilter('all')
            }}
            className="rounded-md border border-[#cdd8ca] bg-[#f4f7f0] px-3 py-1.5 text-[12px] hover:bg-[#eaf0e5]"
          >
            Clear filters
          </button>
        </div>
      ) : (
        <div className="overflow-hidden rounded-[10px] border border-line bg-white shadow-sm">
          <div
            aria-hidden="true"
            className="hidden min-h-[39px] grid-cols-[minmax(0,1fr)_98px_108px_73px_30px] items-center gap-5 px-5 text-[10px] uppercase tracking-wide text-muted lg:grid"
          >
            <span>Publication</span>
            <span>State</span>
            <span>Content changed</span>
            <span className="text-right">Size</span>
            <span />
          </div>
          {groups.map(({ project, publications }) => (
            <ProjectGroup
              key={project.id}
              project={project}
              publications={publications}
              collapsed={query === '' && filter === 'all' && collapsedIds.has(project.id)}
              onToggle={() => toggle(project.id)}
              observation={observation}
              onInspect={setSelected}
            />
          ))}
        </div>
      )}

      <footer className="mt-4 flex flex-wrap items-center justify-between gap-x-5 gap-y-1.5 text-[10px] text-muted">
        <span>Sizes show accepted content. Select a state for observation details.</span>
        <span className="whitespace-nowrap">Page Hub {version}</span>
      </footer>

      {selected && <PublicationDetails publication={selected} observation={observation} onClose={() => setSelected(null)} />}
    </section>
  )
}
