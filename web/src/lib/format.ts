const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']

export function formatBytes(size: number): string {
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KiB`
  if (size < 1024 * 1024 * 1024) return `${(size / (1024 * 1024)).toFixed(1)} MiB`
  return `${(size / (1024 * 1024 * 1024)).toLocaleString('en-US', { maximumFractionDigits: 1 })} GiB`
}

export function formatExactBytes(size: number): string {
  return `${size.toLocaleString('en-US')} bytes`
}

function utcParts(value: string): Date | null {
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return null
  return parsed
}

// Short display dates are rendered from UTC parts with fixed month names so
// catalog timestamps never depend on the browser's locale or time zone.
export function formatShortDate(value: string): string {
  const parsed = utcParts(value)
  if (!parsed) return value
  return `${parsed.getUTCDate()} ${MONTHS[parsed.getUTCMonth()]}`
}

export function formatShortDateTime(value: string): string {
  const parsed = utcParts(value)
  if (!parsed) return value
  const hours = String(parsed.getUTCHours()).padStart(2, '0')
  const minutes = String(parsed.getUTCMinutes()).padStart(2, '0')
  return `${formatShortDate(value)}, ${hours}:${minutes} UTC`
}

export function formatDate(value: string): string {
  const parsed = utcParts(value)
  if (!parsed) return value
  return parsed.toISOString().slice(0, 16).replace('T', ' ') + ' UTC'
}
