import { useQuery } from '@tanstack/react-query'

import { getInventory, getManagerStatus } from './api/client'
import type { components } from './api/generated'
import { Badge } from './components/ui/badge'
import { Card, CardContent, CardHeader } from './components/ui/card'

type BucketObservation = components['schemas']['BucketObservation']
type PublicationObservation = components['schemas']['PublicationObservation']

const statusStyles = {
  reachable: 'bg-emerald-100 text-emerald-800',
  unavailable: 'bg-amber-100 text-amber-800',
  misconfigured: 'bg-rose-100 text-rose-800',
} as const

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

function formatBytes(size: number): string {
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KiB`
  return `${(size / (1024 * 1024)).toFixed(1)} MiB`
}

function formatDate(value: string): string {
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return value
  return parsed.toISOString().slice(0, 16).replace('T', ' ') + ' UTC'
}

function ObservationBadge({ observation }: { observation: PublicationObservation }) {
  return (
    <span className="inline-flex items-center gap-2">
      <Badge className={`border border-transparent ${observedStyles[observation.state]}`}>
        {observedLabels[observation.state]}
      </Badge>
      {observation.statusDetail && (
        <span className="text-xs text-amber-700">{observation.statusDetail}</span>
      )}
    </span>
  )
}

function UsageRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-baseline justify-between gap-4">
      <dt className="text-sm text-slate-500">{label}</dt>
      <dd className="font-mono text-sm">{value}</dd>
    </div>
  )
}

function UsageCard({ observation }: { observation: BucketObservation }) {
  const { usage } = observation
  return (
    <Card aria-label="Storage usage">
      <CardHeader>
        <h2 className="text-lg font-semibold">Storage usage</h2>
        <p className="mt-1 text-sm text-slate-500">
          From the complete bucket observation {formatDate(observation.observedAt)}.
        </p>
      </CardHeader>
      <CardContent>
        <dl className="grid grid-cols-1 gap-2 sm:grid-cols-2">
          <UsageRow label="Quota" value={formatBytes(usage.quotaBytes)} />
          <UsageRow label="Bucket usage" value={formatBytes(usage.totalBytes)} />
          <UsageRow label="Accepted Publications" value={formatBytes(usage.acceptedBytes)} />
          <UsageRow label="Unclaimed storage" value={formatBytes(usage.unclaimedBytes)} />
        </dl>
        {observation.mutationLock !== 'none' && (
          <p className="mt-3 text-xs text-amber-700">
            Storage mutations would be locked ({observation.mutationLock.replace('_', ' ')}) until the findings above are classified.
          </p>
        )}
      </CardContent>
    </Card>
  )
}

function UnavailableUsageCard() {
  return (
    <Card aria-label="Storage usage">
      <CardHeader>
        <h2 className="text-lg font-semibold">Storage usage</h2>
        <p className="mt-1 text-sm text-slate-500">From the complete bucket observation.</p>
      </CardHeader>
      <CardContent>
        <p className="text-sm text-amber-700">
          Usage is unavailable until the first storage scan completes. Published sizes above still come from their accepted manifests.
        </p>
      </CardContent>
    </Card>
  )
}

export function App() {
  const status = useQuery({
    queryKey: ['manager-status'],
    queryFn: getManagerStatus,
    refetchInterval: 30_000,
  })
  const inventory = useQuery({
    queryKey: ['inventory'],
    queryFn: getInventory,
    refetchInterval: 30_000,
  })

  const storageStatus = status.data?.storage.status
  const projects = inventory.data?.projects ?? []
  const observation = inventory.data?.observation ?? undefined

  return (
    <main className="min-h-screen bg-slate-50 text-slate-950">
      <div className="mx-auto flex w-full max-w-5xl flex-col gap-8 px-4 py-8 sm:px-8 sm:py-12">
        <header className="flex flex-col gap-2">
          <p className="text-sm font-semibold uppercase tracking-[0.2em] text-slate-500">Private manager</p>
          <h1 className="text-4xl font-semibold tracking-tight sm:text-5xl">Page Hub</h1>
          <p className="max-w-2xl text-base leading-7 text-slate-600">
            Manage Publications while the independent public reader keeps serving their canonical URLs.
          </p>
        </header>

        <section aria-label="Publication inventory" className="flex flex-col gap-4">
          {inventory.isPending && (
            <Card>
              <CardContent className="py-6 text-sm text-slate-500">Loading inventory…</CardContent>
            </Card>
          )}
          {inventory.isError && (
            <Card>
              <CardContent className="py-6 text-sm text-amber-700">Inventory is unavailable.</CardContent>
            </Card>
          )}
          {!inventory.isPending && !inventory.isError && projects.length === 0 && (
            <Card>
              <CardContent className="py-6 text-sm text-slate-500">
                No Projects are managed yet. Adopt a declared Publication with the plan and commit commands.
              </CardContent>
            </Card>
          )}
          {projects.map((project) => (
            <Card key={project.id}>
              <CardHeader>
                <h2 className="text-lg font-semibold">{project.displayName}</h2>
                {project.description && <p className="mt-1 text-sm text-slate-600">{project.description}</p>}
              </CardHeader>
              <CardContent className="flex flex-col divide-y divide-slate-100">
                {project.publications.length === 0 && (
                  <p className="py-3 text-sm text-slate-500">No Publications in this Project yet.</p>
                )}
                {project.publications.map((publication) => (
                  <div key={publication.id} className="flex flex-col gap-1 py-3 sm:flex-row sm:items-baseline sm:justify-between">
                    <div className="min-w-0">
                      <p className="truncate font-medium">{publication.displayName}</p>
                      <p className="truncate font-mono text-xs text-slate-500">{publication.path}</p>
                      {publication.description && <p className="mt-1 text-sm text-slate-600">{publication.description}</p>}
                      {publication.observation && (
                        <div className="mt-1">
                          <ObservationBadge observation={publication.observation} />
                        </div>
                      )}
                    </div>
                    <div className="flex shrink-0 items-center gap-3 text-xs text-slate-500 sm:flex-col sm:items-end sm:gap-1">
                      <span>{formatBytes(publication.size)}</span>
                      <span>Content changed {formatDate(publication.contentChangedAt)}</span>
                      <Badge className="border border-slate-200 bg-slate-50 text-slate-600">{publication.routingMode}</Badge>
                    </div>
                  </div>
                ))}
              </CardContent>
            </Card>
          ))}
        </section>

        {observation ? <UsageCard observation={observation} /> : !inventory.isPending && !inventory.isError && <UnavailableUsageCard />}

        <Card>
          <CardHeader>
            <h2 className="text-lg font-semibold">Runtime status</h2>
            <p className="mt-1 text-sm text-slate-500">A read-only check confirms that Page Hub can reach its configured bucket.</p>
          </CardHeader>
          <CardContent className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <p className="text-sm font-medium text-slate-500">S3-compatible storage</p>
              {status.isPending && <p className="mt-1 text-lg">Checking…</p>}
              {status.isError && <p className="mt-1 text-lg text-amber-700">Status unavailable</p>}
              {storageStatus && (
                <Badge className={`mt-2 ${statusStyles[storageStatus]}`}>{storageStatus}</Badge>
              )}
            </div>
            <p className="text-sm text-slate-500">Version {status.data?.version ?? '—'}</p>
          </CardContent>
        </Card>
      </div>
    </main>
  )
}
