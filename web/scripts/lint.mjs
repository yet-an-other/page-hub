import { readFileSync, readdirSync, statSync } from 'node:fs'
import { resolve } from 'node:path'

const root = resolve(import.meta.dirname, '..', 'src')
const files = []
function visit(directory) {
  for (const entry of readdirSync(directory)) {
    const file = resolve(directory, entry)
    if (statSync(file).isDirectory()) visit(file)
    else if (/\.(ts|tsx)$/.test(entry)) files.push(file)
  }
}
visit(root)
for (const file of files) {
  const source = readFileSync(file, 'utf8')
  if (/\bconsole\.log\s*\(/.test(source)) {
    throw new Error(`console.log is not allowed in ${file}`)
  }
}
