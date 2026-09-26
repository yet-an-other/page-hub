import type { ReactNode } from 'react'

type IconProps = { className?: string }

function svg(className: string | undefined, children: ReactNode) {
  return (
    <svg
      aria-hidden="true"
      className={className}
      width="16"
      height="16"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.6"
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      {children}
    </svg>
  )
}

export function SearchIcon({ className }: IconProps) {
  return svg(className, <>
    <circle cx="10.5" cy="10.5" r="6.5" />
    <path d="m16 16 4 4" />
  </>)
}

export function ChevronIcon({ className }: IconProps) {
  return svg(className, <path d="m9 5 7 7-7 7" />)
}

export function FolderIcon({ className }: IconProps) {
  return svg(className, <path d="M3 7V5h7l2 3h9v12H3V7Z" />)
}

export function ExternalIcon({ className }: IconProps) {
  return svg(className, <>
    <path d="M14 4h6v6m0-6L10 14" />
    <path d="M10 4H5a1 1 0 0 0-1 1v14a1 1 0 0 0 1 1h14a1 1 0 0 0 1-1v-5" />
  </>)
}

export function LockIcon({ className }: IconProps) {
  return svg(className, <>
    <rect x="5" y="10" width="14" height="11" rx="2" />
    <path d="M8 10V7a4 4 0 0 1 8 0v3m-4 5v2" />
  </>)
}

export function InfoIcon({ className }: IconProps) {
  return svg(className, <>
    <circle cx="12" cy="12" r="9" />
    <path d="M12 11v6m0-11v2" />
  </>)
}

export function RefreshIcon({ className }: IconProps) {
  return svg(className, <>
    <path d="M20 7v5h-5M4 17v-5h5" />
    <path d="M6 7a7 7 0 0 1 12-1l2 6M4 12l2 6a7 7 0 0 0 12-1" />
  </>)
}

export function CloseIcon({ className }: IconProps) {
  return svg(className, <path d="m6 6 12 12M6 18 18 6" />)
}
