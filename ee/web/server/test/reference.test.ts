// The enterprise section of the control plane's configuration reference, held
// to the source that reads it.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// WHY THIS LIVES HERE. web/apps/api/test/config-docs.test.ts holds the rest of
// that page to the community control plane's source, in both directions, and it
// sets this one section aside by its heading, because the community tree is not
// allowed to name the enterprise code and so cannot see these reads. A section
// set aside by one suite and checked by none is a place where a variable can be
// removed from the code and left on the page forever, which is the failure that
// suite exists for. So this suite is the other half, and it asks both
// questions:
//
//   every variable the section names is read by something, and
//   every variable the entry point reads before it listens is in the section.
//
// "Before it listens" is the entry point's own source plus the sealing key's
// reader in the single sign-on package, which are exactly the reads that decide
// whether the process starts. The audit stream's variables are documented on
// their own page and are not this section's.

import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { readFile, readdir } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

import { LICENSE_ENV, ORG_ENV, TRUSTED_KEYS_ENV } from '../src/license.ts'

const here = path.dirname(fileURLToPath(import.meta.url))
const repo = path.join(here, '..', '..', '..', '..')
const docPath = path.join(repo, 'docs', 'src', 'content', 'docs', 'reference', 'control-plane.md')
const SECTION = '## Read by the enterprise edition'

async function tsFiles(dir: string): Promise<string[]> {
  const out: string[] = []
  for (const e of await readdir(dir, { withFileTypes: true })) {
    if (e.name === 'node_modules' || e.name === 'test') continue
    const full = path.join(dir, e.name)
    if (e.isDirectory()) out.push(...(await tsFiles(full)))
    else if (e.name.endsWith('.ts')) out.push(full)
  }
  return out
}

async function namesIn(files: string[]): Promise<Set<string>> {
  const found = new Set<string>()
  for (const file of files) {
    for (const m of (await readFile(file, 'utf8')).matchAll(/\bAF_[A-Z0-9_]+/g)) found.add(m[0])
  }
  return found
}

async function section(): Promise<string> {
  const doc = await readFile(docPath, 'utf8')
  const start = doc.indexOf(`\n${SECTION}\n`)
  // Not a skip. A heading somebody renamed would make this suite read nothing
  // and pass, while the community suite went red for a reason nobody could see.
  assert.ok(start >= 0, `${path.relative(repo, docPath)} has no "${SECTION}" heading, so nothing checks what it documents`)
  const next = doc.indexOf('\n## ', start + 1)
  return doc.slice(start, next < 0 ? undefined : next)
}

describe('the enterprise section of the configuration reference', () => {
  it('names only variables something reads', async () => {
    const text = await section()
    const named = new Set([...text.matchAll(/`(AF_[A-Z0-9_]+)`/g)].map((m) => m[1]!))
    // Either edition counts as a reader: the section says AF_ENTERPRISE_BASE_URL
    // falls back to AF_APP_BASE_URL, and the community control plane is what
    // reads the second.
    const read = new Set([
      ...(await namesIn(await tsFiles(path.join(repo, 'ee', 'web')))),
      ...(await namesIn(await tsFiles(path.join(repo, 'web', 'apps', 'api', 'src')))),
    ])
    const stale = [...named].filter((n) => !read.has(n)).sort()
    assert.ok(named.size >= 4, `the section names ${named.size} variables, which means it was not read`)
    assert.deepEqual(stale, [], `the enterprise section documents variables nothing reads:\n  ${stale.join('\n  ')}`)
  })

  it('names every variable the entry point reads before it listens', async () => {
    // READ AS A POSITION, NOT AS TEXT, AND THE FIRST RUN IS WHY.
    //
    // Scanning the source for the NAME reported AF_AUDIT_STREAM_SINK as
    // missing from this section. The entry point does not read it: register.ts
    // says "Null when AF_AUDIT_STREAM_SINK is unset" in a comment about the
    // handle it returns, and the sink's own variables are documented on the
    // audit stream page. A mention is not a use, and a check that cannot tell
    // them apart would have been answered by documenting a variable here that
    // this page is not responsible for.
    //
    // So a read is a property access on an environment object, or one of the
    // constants license.ts exports for exactly this purpose. Both are
    // positions in the code rather than occurrences of a string.
    const text = await section()
    const sources = [
      ...(await tsFiles(path.join(here, '..', 'src'))),
      path.join(repo, 'ee', 'web', 'sso', 'src', 'secrets.ts'),
    ]
    const read = new Set<string>([LICENSE_ENV, ORG_ENV, TRUSTED_KEYS_ENV])
    for (const file of sources) {
      for (const m of (await readFile(file, 'utf8')).matchAll(/\benv\.(AF_[A-Z0-9_]+)\b/g)) {
        read.add(m[1]!)
      }
    }
    // The positive controls. A regular expression that stopped matching, or an
    // import that stopped resolving, would otherwise leave this test reading an
    // empty set and passing over nothing.
    assert.ok(read.has('AF_EE_SSO_KEY'), 'the sealing key read in the single sign-on package was not seen, so this measured nothing')
    assert.ok(read.has('AF_LICENSE_KEY'), "the licence variable's own constant was not seen, so this measured nothing")
    const missing = [...read].filter((n) => !text.includes(`\`${n}\``)).sort()
    assert.deepEqual(missing, [], `the entry point reads these and the enterprise section does not document them:\n  ${missing.join('\n  ')}`)
  })
})
