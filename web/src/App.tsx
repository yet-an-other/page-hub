import { useEffect, useState } from 'react'
import type { ReactNode } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { getInventory, getManagerStatus, requestRefresh } from './api/client'
import type { components } from './api/generated'
import { Inventory } from './components/Inventory'
import { InfoIcon, LockIcon, RefreshIcon } from './components/icons'
import { Dialog } from './components/ui/dialog'
import { formatBytes, formatDate, formatExactBytes, formatShortDateTime } from './lib/format'

type BucketObservation = components['schemas']['BucketObservation']

function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 border-b border-line py-2.5 text-[12px]">
      <dt className="text-muted">{label}</dt>
      <dd className="m-0 break-all text-right">{children}</dd>
    </div>
  )
}

// One compact header control replaces the standalone usage cards: the
// summary stays visible, exact values and lock state open on demand.
function StorageDetails({
  observation,
  storageStatus,
  version,
  onClose,
}: {
  observation?: BucketObservation
  storageStatus?: components['schemas']['StorageStatus']['status']
  version: string
  onClose: () => void
}) {
  return (
    <Dialog title="Storage observation" onClose={onClose}>
      <p className="mb-4 mt-1 text-[12px] leading-relaxed text-muted">
        Exact values from the latest complete bucket listing. Publication sizes remain accepted manifest values.
      </p>
      {observation ? (
        <dl className="my-4">
          <Fact label="Bucket usage">{formatExactBytes(observation.usage.totalBytes)}</Fact>
          <Fact label="Quota">{formatExactBytes(observation.usage.quotaBytes)}</Fact>
          <Fact label="Accepted Publications">{formatExactBytes(observation.usage.acceptedBytes)}</Fact>
          <Fact label="Unclaimed storage">{formatExactBytes(observation.usage.unclaimedBytes)}</Fact>
          <Fact label="Observed">{formatDate(observation.observedAt)}</Fact>
          <Fact label="Freshness">{observation.stale ? 'Stale, from the last successful scan' : 'Current'}</Fact>
          <Fact label="Storage mutation lock">{observation.mutationLock}</Fact>
          <Fact label="Storage connectivity">{storageStatus ?? 'Checking…'}</Fact>
        </dl>
      ) : (
        <p className="my-4 rounded-md bg-[#faf4e8] px-3 py-2.5 text-[12px] text-[#7c622e]">
          Usage is unavailable until the first successful scan completes. Publication sizes still come from their accepted manifests.
        </p>
      )}
      {observation && observation.mutationLock !== 'none' && (
        <p className="rounded-md bg-[#faf4e8] px-3 py-2.5 text-[12px] text-[#7c622e]">
          Storage mutations are locked until every finding is classified. Observations never change accepted content.
        </p>
      )}
      <p className="mt-4 text-[11px] text-muted">Page Hub {version}</p>
    </Dialog>
  )
}

export function App() {
  const queryClient = useQueryClient()
  const [storageOpen, setStorageOpen] = useState(false)
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
  // Requesting a refresh joins any running scan; when it settles — success
  // or failure — the inventory refetches so the UI reflects the outcome.
  // Cataloged data stays visible throughout, whatever the refresh outcome is.
  const refresh = useMutation({
    mutationFn: requestRefresh,
    onSettled: () => queryClient.invalidateQueries({ queryKey: ['inventory'] }),
  })

  // Opening the inventory requests a background refresh.
  useEffect(() => {
    refresh.mutate()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const storageStatus = status.data?.storage.status
  const observation = inventory.data?.observation ?? undefined
  const refreshing = refresh.isPending || (inventory.data?.refresh.running ?? false)
  const version = status.data?.version ?? '—'
  const lastScan = refreshing
    ? 'Checking storage…'
    : observation
      ? `${observation.stale ? 'Last checked' : 'Checked'} ${formatShortDateTime(observation.observedAt)}`
      : 'Not checked yet'

  return (
    <div className="min-h-screen bg-paper text-ink">
      <header className="flex flex-wrap items-center gap-x-5 gap-y-2 border-b border-line bg-white px-4 py-3 sm:px-8">
        <a href="/" className="flex flex-none items-center gap-2.5 text-[17px] font-bold tracking-tight">
          <span aria-hidden="true" className="grid h-[33px] w-[31px] place-items-center rounded-[9px] bg-moss font-serif text-[23px] text-white">
            P
          </span>
          Page Hub
        </a>
        <span className="flex items-center gap-1.5 border-l border-line pl-5 text-[11px] text-muted max-md:ml-auto max-md:border-l-0 max-md:pl-0">
          <LockIcon className="h-3.5 w-3.5" />
          Private manager
        </span>
        <div className="ml-auto flex items-center gap-4 text-[12px]">
          <button
            type="button"
            onClick={() => setStorageOpen(true)}
            aria-label="View exact storage usage and status"
            title="View exact storage usage and status"
            className="inline-flex items-center gap-2 whitespace-nowrap"
          >
            <span
              aria-hidden="true"
              className={`h-1.5 w-1.5 rounded-full ${
                storageStatus === 'unavailable' || storageStatus === 'misconfigured'
                  ? 'bg-[#a57324] shadow-[0_0_0_3px_#fff4dd]'
                  : 'bg-[#438361] shadow-[0_0_0_3px_#eaf3ec]'
              }`}
            />
            {observation ? (
              <span>
                <strong className="font-semibold">{formatBytes(observation.usage.totalBytes)}</strong>
                <span className="text-muted"> / {formatBytes(observation.usage.quotaBytes)} used</span>
              </span>
            ) : (
              <span className="text-muted">Usage not available</span>
            )}
            <InfoIcon className="h-3.5 w-3.5 text-muted" />
          </button>
          <span className={`whitespace-nowrap text-[11px] ${observation?.stale && !refreshing ? 'text-[#865f25]' : 'text-muted'}`}>
            {lastScan}
          </span>
          <button
            type="button"
            onClick={() => refresh.mutate()}
            disabled={refreshing}
            aria-label="Refresh storage observation"
            title="Refresh storage observation"
            className="inline-grid h-[30px] w-[30px] flex-none place-items-center rounded-md border border-line bg-white text-[#68786c] hover:border-[#a5b7a9] hover:bg-[#edf5ef] hover:text-moss disabled:cursor-wait disabled:opacity-60"
          >
            <RefreshIcon className={`h-4 w-4 ${refreshing ? 'animate-spin' : ''}`} />
          </button>
        </div>
      </header>

      <main className="mx-auto w-full max-w-[1440px] px-4 pb-28 pt-8 sm:px-6 lg:px-10">
        {inventory.isPending ? (
          <p role="status" className="text-[13px] text-muted">
            Loading inventory…
          </p>
        ) : inventory.isError ? (
          <p role="alert" className="text-[13px] text-[#93641d]">
            Inventory is unavailable.
          </p>
        ) : (
          <Inventory
            projects={inventory.data.projects}
            observation={observation}
            refresh={inventory.data.refresh}
            refreshPending={refresh.isPending}
            version={version}
            onOpenStorage={() => setStorageOpen(true)}
          />
        )}
      </main>

      {storageOpen && (
        <StorageDetails observation={observation} storageStatus={storageStatus} version={version} onClose={() => setStorageOpen(false)} />
      )}
    </div>
  )
}
