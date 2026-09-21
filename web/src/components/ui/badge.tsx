import type { HTMLAttributes } from 'react'

import { cn } from '../../lib/cn'

export function Badge({ className, ...props }: HTMLAttributes<HTMLSpanElement>) {
  return (
    <span
      className={cn('inline-flex items-center rounded-full px-3 py-1 text-sm font-medium', className)}
      {...props}
    />
  )
}
