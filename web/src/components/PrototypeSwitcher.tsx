// THROWAWAY. Only enabled for development or an explicit prototype build.
import { useEffect } from 'react'
import type { Variant } from './inventory-prototype/InventoryPrototype'

const options: { key: Variant; name: string }[] = [
  { key: 'A', name: 'Inventory' },
  { key: 'B', name: 'Reading list' },
  { key: 'C', name: 'Project index' },
]

export function PrototypeSwitcher({ current, onChange, openState }: { current: Variant; onChange: (variant: Variant) => void; openState: () => void }) {
  const index = options.findIndex(option => option.key === current)
  const cycle = (direction: number) => onChange(options[(index + direction + options.length) % options.length].key)
  useEffect(() => {
    const keydown = (event: KeyboardEvent) => {
      if (event.altKey || event.ctrlKey || event.metaKey || document.querySelector('dialog[open]')) return
      if ((event.target as HTMLElement).closest('input, textarea, select, [contenteditable]:not([contenteditable="false"])')) return
      if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') {
        event.preventDefault()
        onChange(options[(index + (event.key === 'ArrowRight' ? 1 : -1) + options.length) % options.length].key)
      }
      if (event.key === '/') {
        event.preventDefault()
        document.querySelector<HTMLInputElement>('.ip-search input')?.focus()
      }
    }
    window.addEventListener('keydown', keydown)
    return () => window.removeEventListener('keydown', keydown)
  }, [index, onChange])
  if (!import.meta.env.DEV && import.meta.env.MODE !== 'prototype') return null
  return <nav className="ip-switcher" aria-label="Prototype variants">
    <span className="ip-switcher-tag">Prototype</span>
    <button aria-label="Previous variant" onClick={() => cycle(-1)}>←</button>
    <span className="ip-switcher-name" aria-live="polite"><strong>{current}</strong> {options[index].name}<small>{index + 1} / 3</small></span>
    <button aria-label="Next variant" onClick={() => cycle(1)}>→</button>
    <button className="ip-switcher-settings" aria-label="Prototype controls and state" onClick={openState}>⋯</button>
  </nav>
}
