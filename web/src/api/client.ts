import type { components } from './generated'

type ManagerStatus = components['schemas']['ManagerStatus']

export async function getManagerStatus(): Promise<ManagerStatus> {
  const response = await fetch('/_page-hub/api/v1/status', {
    headers: { Accept: 'application/json' },
    credentials: 'same-origin',
  })
  if (!response.ok) throw new Error(`Manager status request failed (${response.status})`)
  return (await response.json()) as ManagerStatus
}
