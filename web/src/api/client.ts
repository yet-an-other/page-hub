import type { components } from './generated'

type ManagerStatus = components['schemas']['ManagerStatus']
type Inventory = components['schemas']['Inventory']

export async function getManagerStatus(): Promise<ManagerStatus> {
  const response = await fetch('/_page-hub/api/v1/status', {
    headers: { Accept: 'application/json' },
    credentials: 'same-origin',
  })
  if (!response.ok) throw new Error(`Manager status request failed (${response.status})`)
  return (await response.json()) as ManagerStatus
}

export async function getInventory(): Promise<Inventory> {
  const response = await fetch('/_page-hub/api/v1/inventory', {
    headers: { Accept: 'application/json' },
    credentials: 'same-origin',
  })
  if (!response.ok) throw new Error(`Inventory request failed (${response.status})`)
  return (await response.json()) as Inventory
}
