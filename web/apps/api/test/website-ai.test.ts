import assert from 'node:assert/strict'
import test from 'node:test'
import { emptyWebsiteDocument } from '@antifailure/website'
import { applyWebsiteProposal, requestWebsiteProposal, websitePromptInput } from '../src/admin/website-ai.ts'

const input = websitePromptInput.parse({
  page: '/', prompt: 'Make the headline clearer', targets: ['hero'], fontKeys: ['brand-sans'],
  fields: [
    { key: 'hero.title', label: 'Headline', kind: 'text', value: 'Know what happens before you deploy.' },
    { key: 'hero.icon', label: 'Marker', kind: 'select', value: 'circle', options: [{ label: 'Circle', value: 'circle' }, { label: 'Square', value: 'square' }] },
  ],
})

function providerResponse(value: unknown) {
  return new Response(JSON.stringify({ content: [{ type: 'text', text: JSON.stringify(value) }], usage: { input_tokens: 173, output_tokens: 54 }, stop_reason: 'end_turn' }), { status: 200 })
}

test('bounded model response becomes a reviewable valid draft edit', async () => {
  let request: Record<string, unknown> = {}
  const fetcher = async (_url: string | URL | Request, init?: RequestInit) => {
    request = JSON.parse(String(init?.body)) as Record<string, unknown>
    assert.equal(init?.headers && (init.headers as Record<string, string>)['x-api-key'], 'test-key')
    return providerResponse({ message: 'The headline now names the action.', edits: [{ key: 'hero.title', value: 'Rehearse the change before it ships.' }], styles: [{ target: 'hero', breakpoint: 'mobile', property: 'fontSize', value: 48 }], actions: [] })
  }
  const proposal = await requestWebsiteProposal(input, 'test-key', fetcher as typeof fetch)
  assert.equal(request.model, 'claude-haiku-4-5-20251001')
  assert.equal(request.max_tokens, 1800)
  assert.deepEqual(proposal.usage, { inputTokens: 173, outputTokens: 54 })
  const document = applyWebsiteProposal(emptyWebsiteDocument(), input, proposal, [{ key: 'hero.title', label: 'Headline', kind: 'text', sectionId: 'hero', defaultValue: 'Know what happens before you deploy.' }])
  assert.equal(document.fields['hero.title'], 'Rehearse the change before it ships.')
  assert.equal(document.styles.hero?.mobile?.fontSize, 48)
})

test('a whole-page Product icon alignment request produces a scoped label override', async () => {
  const requestInput = websitePromptInput.parse({
    page: '/product', scope: 'page',
    prompt: 'Make this whole page cleaner and make the bottom of every icon align with the bottom of the text next to it',
    targets: ['page-product'], fontKeys: [],
    fields: [{ key: 'page.product.text.heading', label: 'h1 · Product overview', kind: 'text', value: 'See how your change behaves before you deploy.' }],
  })
  let body: Record<string, unknown> = {}
  const proposal = await requestWebsiteProposal(requestInput, 'test-key', async (_url, init) => {
    body = JSON.parse(String(init?.body)) as Record<string, unknown>
    return providerResponse({ message: 'The label icons now share the text baseline.', edits: [],
      styles: [{ target: 'page-product', breakpoint: 'desktop', property: 'iconAlign', value: 'end' }], actions: [] })
  })
  assert.equal(body.model, 'claude-sonnet-4-6')
  assert.equal(body.max_tokens, 4000)
  const messages = body.messages as Array<{ content: string }>
  assert.equal((JSON.parse(messages.at(-1)!.content) as { scope: string }).scope, 'page')
  const document = applyWebsiteProposal(emptyWebsiteDocument(), requestInput, proposal, [{
    key: 'page.product.text.heading', label: 'h1', kind: 'text', sectionId: 'page-product',
    defaultValue: 'See how your change behaves before you deploy.',
  }])
  assert.equal(document.styles['page-product']?.desktop?.iconAlign, 'end')
})

test('a prompt can create one private article draft without taking an existing route', async () => {
  const pageInput = websitePromptInput.parse({ ...input, prompt: 'Create a new article about safe releases',
    existingPaths: ['/blog', '/blog/what-staging-misses-about-migrations'] })
  const page = { kind: 'post', path: '/blog/release-rehearsals', title: 'What a release rehearsal checks',
    description: 'A practical guide to checking changes before deployment.',
    summary: 'How Antifailure checks a change in an isolated production twin.',
    body: 'A release rehearsal runs the proposed change in an isolated environment.\n\nReview the findings before deploying.',
    tags: 'Engineering, Releases' }
  const proposal = await requestWebsiteProposal(pageInput, 'test-key', async () =>
    providerResponse({ message: 'Review this new article draft.', edits: [], styles: [], actions: [], pages: [page] }))
  const document = applyWebsiteProposal(emptyWebsiteDocument(), pageInput, proposal, [])
  assert.deepEqual(document.pages, [{ path: page.path, kind: 'post' }])
  assert.equal(document.fields['page.blog-release-rehearsals.title'], page.title)
  assert.equal((document.fields['page.blog-release-rehearsals.body'] as { content: unknown[] }).content.length, 2)
  for (const badPath of ['/blog/what-staging-misses-about-migrations', '/admin/new', '/product']) {
    await assert.rejects(() => requestWebsiteProposal(pageInput, 'test-key', async () => providerResponse({
      message: 'Draft', edits: [], styles: [], actions: [], pages: [{ ...page, path: badPath }],
    })))
  }
  const colliding = websitePromptInput.parse({ ...pageInput, existingPaths: ['/guides/deploy-safely'] })
  await assert.rejects(() => requestWebsiteProposal(colliding, 'test-key', async () => providerResponse({
    message: 'Draft', edits: [], styles: [], actions: [], pages: [{ ...page, kind: 'page', path: '/guides-deploy-safely' }],
  })))
})

test('unknown fields and unsafe style values never reach the draft', async () => {
  for (const value of [
    { message: 'Done', edits: [{ key: 'header.logo', value: 'Changed' }], styles: [], actions: [] },
    { message: 'Done', edits: [{ key: 'hero.icon', value: 'hexagon' }], styles: [], actions: [] },
    { message: 'Done', edits: [{ key: 'hero.title', value: 'A seamless — revolutionary product.' }], styles: [], actions: [] },
    { message: 'Done', edits: [], styles: [{ target: 'hero', breakpoint: 'desktop', property: 'color', value: 'javascript:alert(1)' }], actions: [] },
    { message: 'Done', edits: [], styles: [], actions: [{ operation: 'hide-section', sectionId: 'footer', after: '', kind: '', heading: '', body: '' }] },
  ]) {
    await assert.rejects(() => requestWebsiteProposal(input, 'test-key', async () => providerResponse(value)))
  }
})

test('provider failure leaves the submitted draft untouched', async () => {
  const before = emptyWebsiteDocument()
  await assert.rejects(() => requestWebsiteProposal(input, 'test-key', async () => new Response('Unavailable', { status: 503 })))
  assert.deepEqual(before, emptyWebsiteDocument())
})
