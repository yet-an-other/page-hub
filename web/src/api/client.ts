import type { components } from './generated'
import { sampleInventory, sampleStatus } from '../components/inventory-prototype/fixtures'

type ManagerStatus = components['schemas']['ManagerStatus']
type Inventory = components['schemas']['Inventory']
type RefreshState = components['schemas']['RefreshState']

export async function getManagerStatus(): Promise<ManagerStatus> {
  if (import.meta.env.MODE === 'prototype') return structuredClone(sampleStatus)
  const response = await fetch('/_page-hub/api/v1/status', {
    headers: { Accept: 'application/json' },
    credentials: 'same-origin',
  })
  if (!response.ok) throw new Error(`Manager status request failed (${response.status})`)
  return (await response.json()) as ManagerStatus
}

export async function getInventory(): Promise<Inventory> {
  if (import.meta.env.MODE === 'prototype') return sampleInventory()
  const response = await fetch('/_page-hub/api/v1/inventory', {
    headers: { Accept: 'application/json' },
    credentials: 'same-origin',
  })
  if (!response.ok) throw new Error(`Inventory request failed (${response.status})`)
  return (await response.json()) as Inventory
}

// requestRefresh asks Page Hub to run a storage observation. It joins an
// already running scan and answers with the resulting refresh state.
export async function requestRefresh(): Promise<RefreshState> {
  if (import.meta.env.MODE === 'prototype') {
    await new Promise(resolve => setTimeout(resolve, 800))
    return { running: false, lastOutcome: 'succeeded' }
  }
  const response = await fetch('/_page-hub/api/v1/refresh', {
    method: 'POST',
    headers: { Accept: 'application/json' },
    credentials: 'same-origin',
  })
  if (!response.ok) throw new Error(`Refresh request failed (${response.status})`)
  return (await response.json()) as RefreshState
}
