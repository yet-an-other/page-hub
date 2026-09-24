import { useMemo, useState } from 'react'

import type { components } from '../api/generated'
import { Badge } from './ui/badge'
import { formatBytes, formatDate } from '../lib/format'

type InventoryProject = components['schemas']['InventoryProject']
type InventoryPublication = components['schemas']['InventoryPublication']
type BucketObservation = components['schemas']['BucketObservation']

const observedStyles = {
  in_sync: 'bg-emerald-100 text-emerald-800',
  drifted: 'bg-amber-100 text-amber-800',
  missing: 'bg-rose-100 text-rose-800',
} as const

const observedLabels = {
  in_sync: 'in sync',
  drifted: 'drifted',
  missing: 'missing',
} as const

// previewHref addresses the authenticated preview route for one Publication
function previewHref(publicationPath: string): string {
  return `/_page-hub/preview/${publicationPath.split('/').map(encodeURIComponent).join('/')}`
}

// publicPathText renders the canonical public path of a Publication.
// Cataloged paths already include the Project prefix. The entry point is
// shown unless it is the directory index of the Publication's own path.
function publicPathText(publication: InventoryPublication): string {
  const root = `/${publication.path}`
  if (publication.entryPoint === `${publication.path}/index.html`) return `${root}/`
  const relativeEntry = publication.entryPoint.startsWith(`${publication.path}/`)
    ? publication.entryPoint.slice(publication.path.length + 1)
    : publication.entryPoint
  return `${root}/${relativeEntry}`
}

function matchesQuery(query: string, values: Array<string | undefined>): boolean {
  return values.some((value) => value?.toLowerCase().includes(query))
}

// PublicationState renders the observed state, the status detail, the
// observation time, and staleness. Cataloged values stay visible whatever
// the observation state is.
function PublicationState({ publication, observation }: { publication: InventoryPublication; observation?: BucketObservation }) {
  if (!publication.observation) {
    return (
      <div className="flex flex-col gap-0.5">
        <span className="text-xs text-slate-500">Not observed yet</span>
        {observation && <span className="text-xs text-slate-500">Last scan {formatDate(observation.observedAt)}</span>}
      </div>
    )
  }
  const state = publication.observation.state
  return (
    <div className="flex min-w-0 flex-col gap-0.5">
      <span className="flex flex-wrap items-center gap-2">
        <Badge className={`border border-transparent ${observedStyles[state]}`}>{observedLabels[state]}</Badge>
        {observation?.stale && <span className="text-xs font-medium text-amber-700">Observation is stale</span>}
      </span>
      {publication.observation.statusDetail && (
        <span className="text-xs text-amber-700">{publication.observation.statusDetail}</span>
      )}
      {observation && <span className="text-xs text-slate-500">Observed {formatDate(observation.observedAt)}</span>}
    </div>
  )
}

function PublicationRows({ publications, observation }: { publications: InventoryPublication[]; observation?: BucketObservation }) {
  if (publications.length === 0) {
    return (
      <tr>
        <td colSpan={6} className="px-4 py-4 text-sm text-slate-500 sm:px-6">
          No Publications in this Project yet.
        </td>
      </tr>
    )
  }
  return (
    <>
      {publications.map((publication) => (
        <tr key={publication.id} className="hover:bg-slate-50">
          <td className="px-4 py-3 align-top sm:px-6">
            <p className="font-medium">{publication.displayName}</p>
            <a
              href={previewHref(publication.path)}
              target="_blank"
              rel="noopener"
              title="Preview in Page Hub"
              className="mt-0.5 block truncate font-mono text-xs text-emerald-800 underline decoration-emerald-200 underline-offset-2 hover:decoration-emerald-500"
            >
              {publicPathText(publication)}
            </a>
          </td>
          <td className="hidden max-w-56 px-4 py-3 align-top text-sm text-slate-600 md:table-cell sm:px-6">
            {publication.description || <span className="italic text-slate-500">No description</span>}
          </td>
          <td className="px-4 py-3 align-top sm:px-6">
            <PublicationState publication={publication} observation={observation} />
          </td>
          <td className="hidden px-4 py-3 align-top text-sm text-slate-600 lg:table-cell sm:px-6">
            Content changed {formatDate(publication.contentChangedAt)}
          </td>
          <td className="px-4 py-3 text-right align-top font-mono text-sm sm:px-6">{formatBytes(publication.size)}</td>
          <td className="px-4 py-3 text-right align-top sm:px-6">
            <a
              href={publication.canonicalUrl}
              target="_blank"
              rel="noopener noreferrer"
              aria-label={`Open ${publication.displayName} public page`}
              title="Open public page"
              className="inline-flex rounded-md border border-slate-300 bg-white p-1.5 text-slate-600 hover:text-emerald-800 hover:border-emerald-300"
            >
              <svg aria-hidden="true" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                <path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" />
                <polyline points="15 3 21 3 21 9" />
                <line x1="10" y1="14" x2="21" y2="3" />
              </svg>
            </a>
          </td>
        </tr>
      ))}
    </>
  )
}

function ProjectGroup({
  project,
  publications,
  collapsed,
  onToggle,
  observation,
}: {
  project: InventoryProject
  publications: InventoryPublication[]
  collapsed: boolean
  onToggle: () => void
  observation?: BucketObservation
}) {
  const acceptedBytes = project.publications.reduce((sum, publication) => sum + publication.size, 0)
  const count = project.publications.length
  return (
    <tbody id={`project-${project.id}`}>
      <tr className="bg-emerald-50/70">
        <td colSpan={6} className="border-y border-emerald-100 px-4 py-3 sm:px-6">
          <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
            <button
              type="button"
              onClick={onToggle}
              aria-expanded={!collapsed}
              aria-controls={`project-${project.id}`}
              className="flex min-w-0 flex-1 items-center gap-2 text-left"
            >
              <svg
                aria-hidden="true"
                width="12"
                height="12"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="3"
                className={`shrink-0 text-slate-500 transition-transform ${collapsed ? '' : 'rotate-90'}`}
              >
                <polyline points="9 18 15 12 9 6" />
              </svg>
              <span className="min-w-0">
                <span className="font-semibold">
                  {project.displayName}{' '}
                  <span className="font-mono text-xs font-normal text-slate-500">/{project.prefix}/</span>
                </span>
                {project.description && <span className="block text-sm text-slate-600">{project.description}</span>}
              </span>
            </button>
            <span className="shrink-0 text-xs text-slate-500">
              {count} publication{count === 1 ? '' : 's'} · {formatBytes(acceptedBytes)}
            </span>
          </div>
        </td>
      </tr>
      {!collapsed && <PublicationRows publications={publications} observation={observation} />}
    </tbody>
  )
}

export function Inventory({ projects, observation }: { projects: InventoryProject[]; observation?: BucketObservation }) {
  const [searchText, setSearchText] = useState('')
  // Collapsed Projects stay collapsed across searches: a search expands
  // matching groups visually without discarding the operator's choices.
  const [collapsedIds, setCollapsedIds] = useState<ReadonlySet<string>>(new Set())

  const query = searchText.trim().toLowerCase()
  const groups = useMemo(() => {
    return projects
      .map((project) => {
        if (!query) return { project, publications: project.publications }
        const projectMatches = matchesQuery(query, [project.displayName, project.prefix, project.description])
        const matchingPublications = project.publications.filter((publication) =>
          matchesQuery(query, [publication.displayName, publication.path, publication.entryPoint, publication.description]),
        )
        if (!projectMatches && matchingPublications.length === 0) return null
        return { project, publications: projectMatches ? project.publications : matchingPublications }
      })
      .filter((group): group is { project: InventoryProject; publications: InventoryPublication[] } => group !== null)
  }, [projects, query])

  const publicationCount = groups.reduce((sum, group) => sum + group.publications.length, 0)
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

  return (
    <section aria-label="Publication inventory" className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-3">
        <input
          type="search"
          value={searchText}
          onChange={(event) => setSearchText(event.target.value)}
          placeholder="Search projects and publications"
          aria-label="Search projects and publications"
          className="h-9 w-full max-w-96 rounded-lg border border-slate-300 bg-white px-3 text-sm focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-emerald-700"
        />
        <span className="text-sm text-slate-500" role="status">
          {groups.length} project{groups.length === 1 ? '' : 's'} · {publicationCount} publication{publicationCount === 1 ? '' : 's'}
        </span>
      </div>

      <div className="overflow-x-auto rounded-xl border border-slate-200 bg-white">
        <table className="w-full border-collapse text-left text-sm">
          <thead>
            <tr className="border-b border-slate-200 bg-slate-50 text-xs uppercase tracking-wide text-slate-500">
              <th scope="col" className="px-4 py-2.5 font-semibold sm:px-6">Publication</th>
              <th scope="col" className="hidden px-4 py-2.5 font-semibold md:table-cell sm:px-6">Private description</th>
              <th scope="col" className="px-4 py-2.5 font-semibold sm:px-6">State</th>
              <th scope="col" className="hidden px-4 py-2.5 font-semibold lg:table-cell sm:px-6">Content changed</th>
              <th scope="col" className="px-4 py-2.5 text-right font-semibold sm:px-6">Size</th>
              <th scope="col" className="px-4 py-2.5 font-semibold sm:px-6">
                <span className="sr-only">Open public page</span>
              </th>
            </tr>
          </thead>
          {groups.map(({ project, publications }) => (
            <ProjectGroup
              key={project.id}
              project={project}
              publications={publications}
              collapsed={query === '' && collapsedIds.has(project.id)}
              onToggle={() => toggle(project.id)}
              observation={observation}
            />
          ))}
        </table>
        {groups.length === 0 && (
          <p className="px-6 py-10 text-center text-sm text-slate-500">No Projects or Publications match this search.</p>
        )}
      </div>
    </section>
  )
}
