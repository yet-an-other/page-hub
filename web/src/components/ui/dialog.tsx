import { useEffect, useId, useRef } from 'react'
import type { ReactNode } from 'react'

import { CloseIcon } from '../icons'

const closeButton =
  'inline-grid h-[30px] w-[30px] flex-none place-items-center rounded-md border border-line bg-white text-[#68786c] hover:border-[#a5b7a9] hover:bg-[#edf5ef] hover:text-moss'

// Modal dialog on the native <dialog> element: Escape cancels through
// onCancel, and a backdrop click closes without closing on inner clicks.
export function Dialog({ title, onClose, children }: { title: string; onClose: () => void; children: ReactNode }) {
  const ref = useRef<HTMLDialogElement>(null)
  const titleId = useId()
  useEffect(() => {
    ref.current?.showModal()
  }, [])
  return (
    <dialog
      ref={ref}
      aria-labelledby={titleId}
      onCancel={onClose}
      onClick={(event) => {
        if (event.target === event.currentTarget) onClose()
      }}
      className="m-auto max-h-[calc(100dvh-3rem)] w-[min(540px,calc(100vw-2rem))] overflow-y-auto rounded-[13px] border border-line bg-white p-6 text-ink shadow-2xl"
    >
      <div className="mb-4 flex items-start justify-between gap-4">
        <h2 id={titleId} className="mt-0.5 break-words text-[19px] font-semibold">
          {title}
        </h2>
        <button type="button" onClick={onClose} aria-label="Close details" className={closeButton}>
          <CloseIcon className="h-4 w-4" />
        </button>
      </div>
      {children}
    </dialog>
  )
}
