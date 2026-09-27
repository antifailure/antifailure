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
