// The configuration reference, against the configuration.
//
// A page listing environment variables is exactly the kind of documentation
// that goes stale the first time somebody adds one, and nothing says so. So the
// page is checked against the source: every AF_ variable the process reads has
// to appear there, and every variable the page describes has to be one
// something actually reads.

import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { readFile, readdir } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const srcDir = path.join(here, '..', 'src')
const docPath = path.join(
  here, '..', '..', '..', '..', 'docs', 'src', 'content', 'docs', 'reference', 'control-plane.md',
)

async function sourceFiles(dir: string): Promise<string[]> {
  const entries = await readdir(dir, { withFileTypes: true })
  const out: string[] = []
  for (const e of entries) {
    const full = path.join(dir, e.name)
    if (e.isDirectory()) out.push(...(await sourceFiles(full)))
    else if (e.name.endsWith('.ts')) out.push(full)
  }
  return out
}

/** Every AF_ variable the process reads, from the source rather than a list
 *  somebody maintains by hand. */
async function readVariables(): Promise<Set<string>> {
  const found = new Set<string>()
  for (const file of await sourceFiles(srcDir)) {
    const text = await readFile(file, 'utf8')
    for (const m of text.matchAll(/\bAF_[A-Z0-9_]+/g)) found.add(m[0])
  }
  return found
}

describe('the control plane configuration reference', () => {
  it('describes every variable the process reads', async () => {
    const doc = await readFile(docPath, 'utf8')
    const undocumented = [...(await readVariables())].filter((v) => !doc.includes(`\`${v}\``)).sort()
    assert.deepEqual(
      undocumented,
      [],
      `these variables are read but not documented:\n  ${undocumented.join('\n  ')}\n` +
        `Add them to ${path.relative(process.cwd(), docPath)}.`,
    )
  })

  it('describes nothing the process does not read', async () => {
    // The other direction. A variable removed from the code and left on the
    // page is an operator setting something that does nothing.
    const doc = await readFile(docPath, 'utf8')
    const read = await readVariables()
    const described = [...doc.matchAll(/`(AF_[A-Z0-9_]+)`/g)].map((m) => m[1]!)
    const stale = [...new Set(described)].filter((v) => !read.has(v)).sort()
    assert.deepEqual(
      stale,
      [],
      `these variables are documented but nothing reads them:\n  ${stale.join('\n  ')}`,
    )
  })

  it('finds variables at all, so a broken scan cannot pass quietly', async () => {
    // The negative control. If the scan stops matching, both tests above go
    // green having compared two empty sets.
    const read = await readVariables()
    assert.ok(read.size >= 8, `the source scan found only ${read.size} variables`)
    assert.ok(read.has('AF_DATABASE_URL'), 'the scan did not find the one variable that is always read')
  })
})
