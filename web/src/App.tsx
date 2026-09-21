import { useQuery } from '@tanstack/react-query'

import { getManagerStatus } from './api/client'
import { Badge } from './components/ui/badge'
import { Card, CardContent, CardHeader } from './components/ui/card'

const statusStyles = {
  reachable: 'bg-emerald-100 text-emerald-800',
  unavailable: 'bg-amber-100 text-amber-800',
  misconfigured: 'bg-rose-100 text-rose-800',
} as const

export function App() {
  const status = useQuery({
    queryKey: ['manager-status'],
    queryFn: getManagerStatus,
    refetchInterval: 30_000,
  })

  const storageStatus = status.data?.storage.status
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
