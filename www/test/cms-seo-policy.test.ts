import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { emptyWebsiteDocument, type WebsiteDocument } from '@antifailure/website'
import { assessNavigation, cmsNavigationWasEdited } from '../scripts/cms-seo-policy.mjs'
import { documentHash } from '../scripts/cms-snapshot.mjs'

function policy(change: (document: WebsiteDocument) => void, revision = 1): boolean {
  const document = emptyWebsiteDocument()
  change(document)
  const contentHash = documentHash(document)
  return cmsNavigationWasEdited({ document, revision, contentHash }, { revision, contentHash })
}

describe('CMS navigation SEO contract', () => {
  it('keeps ordinary source CI and unchanged published documents strict', () => {
    assert.equal(policy(() => {}, 0), false)
    assert.equal(policy(() => {}), false)
  })

  it('keeps typography, copy, page blocks, ordering and added links strict', () => {
    assert.equal(policy((document) => {
      document.fields['header.menus.product.text'] = 'Platform'
      document.fields['hero.title'] = 'A completely new homepage'
      document.styles.header = { desktop: { fontSize: 24 } }
      document.sections.hidden = ['hero', 'migrations']
      document.collections['header.menus'] = {
        hidden: [], moves: [{ id: 'pricing', after: null }],
        custom: [{ id: 'custom-new', fields: { text: 'Book a demo', href: '/request-demo' } }],
      }
    }), false)
  })

  it('recognizes deliberate removals throughout nested header and footer collections', () => {
    for (const key of [
      'header.menus', 'header.actions', 'header.menus.product.sections',
      'header.menus.product.sections.core.items', 'header.menus.product.featured',
      'footer.columns', 'footer.columns.company.items', 'footer.legal',
    ]) {
      assert.equal(policy((document) => {
        document.collections[key] = { hidden: ['about'], moves: [], custom: [] }
      }), true, key)
    }
  })

  it('recognizes changed source destinations and a hidden header or footer', () => {
    for (const key of [
      'header.actions.demo.href', 'header.menus.docs.href',
      'header.menus.product.sections.core.items.twins.href',
      'header.menus.product.featured.migrations.href', 'header.github.href',
      'footer.columns.company.items.about.href', 'footer.legal.privacy.href',
    ]) {
      assert.equal(policy((document) => { document.fields[key] = '/request-demo' }), true, key)
    }
    for (const section of ['header', 'footer']) {
      assert.equal(policy((document) => { document.sections.hidden = [section] }), true)
    }
  })

  it('recognizes an empty menu label that the renderer removes', () => {
    assert.equal(policy((document) => { document.fields['header.menus.product.text'] = ' ' }), true)
  })

  it('does not grant an exemption for an unrelated or stale collection key', () => {
    for (const key of ['hero.actions', 'footer.decorations', 'header.menus.product.unknown']) {
      assert.equal(policy((document) => {
        document.collections[key] = { hidden: ['about'], moves: [], custom: [] }
      }), false, key)
    }
    assert.equal(policy((document) => { document.fields['hero.demo.href'] = '/contact' }), false)
  })

  it('refuses a different snapshot than the document actually rendered', () => {
    const document = emptyWebsiteDocument()
    document.sections.hidden = ['header']
    const contentHash = documentHash(document)
    assert.throws(() => cmsNavigationWasEdited({ document, revision: 2, contentHash }, { revision: 1, contentHash }), /does not match/)
    assert.throws(() => cmsNavigationWasEdited({ document, revision: 2, contentHash }, { revision: 2, contentHash: 'a'.repeat(64) }), /does not match/)
  })

  it('keeps missing required links and unreachable source pages as failures', () => {
    const result = assessNavigation({ customized: false, missingHomeRoutes: ['/about'], unreachableRoutes: ['/blog'], brokenDestinations: ['/missing'] })
    assert.deepEqual(result.missingHomeRoutes, ['/about'])
    assert.deepEqual(result.unreachableRoutes, ['/blog'])
    assert.deepEqual(result.brokenDestinations, ['/missing'])
    assert.deepEqual(result.notices, { missingHomeRoutes: [], unreachableRoutes: [] })
  })

  it('reports deliberate omissions as notes while broken destinations still fail', () => {
    const result = assessNavigation({ customized: true, missingHomeRoutes: ['/about'], unreachableRoutes: ['/blog'], brokenDestinations: ['/missing'] })
    assert.deepEqual(result.missingHomeRoutes, [])
    assert.deepEqual(result.unreachableRoutes, [])
    assert.deepEqual(result.brokenDestinations, ['/missing'])
    assert.deepEqual(result.notices, { missingHomeRoutes: ['/about'], unreachableRoutes: ['/blog'] })
  })
})
