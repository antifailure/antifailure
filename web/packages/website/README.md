# Website document model

`@antifailure/website` is the shared, dependency-free contract used by the website,
admin editor, and API. It exports TypeScript source for the existing workspace
toolchain and runs its tests directly on Node 24.

## Source defaults and explicit edits

Store only overrides. Components continue to own their default text, media,
styles, and collections. The preview reports those current defaults through a
`WebsiteManifest`; it does not copy them into the stored `WebsiteDocument`.

```ts
const document = setFieldOverride(emptyWebsiteDocument(), 'hero.title', 'My title')
resolveField(document, 'hero.title', 'New source title') // My title
resolveField(document, 'hero.subtitle', 'New source subtitle') // New source subtitle

const reset = setFieldOverride(document, 'hero.title', undefined)
resolveField(reset, 'hero.title', 'New source title') // New source title
```

Passing the current source default to `setFieldOverride` also removes an override.
Field resolution falls back to source when an override has an incompatible type.
An explicit `null` media override removes the source image/video; resetting the
override restores it. Media references optionally carry their verified `kind`
because uploaded asset URLs use UUID routes rather than filename extensions.
For rich text, supply a structured document as the fallback, including when the
source text originally came from a string. Ordinary text controls stay strings.
Orphaned keys are harmless and remain available for the editor to display/reset.

Collections use stable item IDs and field keys in the form
`collectionKey.itemId.fieldName`. `resolveCollection` supports flat source items
and manifest items with a nested `fields` object. Custom items cannot replace an
existing source ID. New source items remain visible automatically unless hidden.

`resolveOrder` applies relative moves rather than saved source indexes. It keeps
new source IDs, ignores missing anchors, ignores cyclic moves as a unit, and
applies hiding after resolving anchors. Pass custom section anchors as moves
alongside explicit moves, with explicit moves last. The caller selects the
appropriate section group before resolving its order.

Shape and divider blocks keep their geometry in source. `${id}.shape` selects
from `CUSTOM_SHAPES`; `${id}.variant` selects from `DIVIDER_VARIANTS`, with an
optional `${id}.label` for divider text. Use `resolveCustomShape` and
`resolveDividerVariant` when rendering to fall back from malformed names. The
existing color, background, spacing, and size styles control their presentation;
Shape choices never become executable SVG or HTML.

Embed blocks may store HTML, CSS, and JavaScript as bounded plain string fields.
Manifest controls identify these as `kind: 'code'` with an optional `language`.
The model does not execute code: the renderer must use an isolated sandboxed
iframe, never raw HTML insertion into the surrounding website. Placement uses
the existing bounded offsets plus `position` (`relative`, `absolute`, `fixed`)
and an integer `zIndex` between 0 and 100, not arbitrary CSS declarations.

## Validation boundaries

- Writes use `assertWebsiteDocument` or `validateWebsiteDocument`; every invalid
  field, style property, or unknown key rejects the write.
- Reads use `normalizeWebsiteDocument`. A malformed field or item is dropped
  without erasing its valid siblings. Warnings identify the affected paths. A
  document exceeding the global 1 MiB resource limit is rejected as a whole.
- Rich text is a small structured subset: paragraphs, text, breaks, lists, and
  the permitted marks. Render it as React elements. Never pass it to raw HTML.
  Strip Tiptap's unused default node attributes before storing it.
- Root-relative builtin media paths reject traversal, protocol-relative URLs,
  controls, and encoded forms of those inputs. Uploaded assets use UUIDs.
- String fields are text, not prevalidated URLs. Renderers must use `safeHref`
  before using a resolved string as a link destination.
- Font styles contain a font choice key or `asset:UUID`. Resolve choice keys
  through the manifest; do not treat them as arbitrary CSS.
- Desktop, tablet, and mobile styles are independent. Desktop overrides never
  become the fallback for a smaller viewport. Missing overrides retain the
  component's own responsive design.
- Numeric style values are pixels except unitless `lineHeight`/`opacity` and
  percentage `focalX`/`focalY`. `STYLE_NUMBER_BOUNDS` is shared with controls.

`referencedAssets` returns unique sorted asset UUIDs, including collection media
and uploaded fonts. `stableStringify` is deterministic JSON for equality and
caller-owned hashing; it rejects non-JSON values and never invokes getters or
`toJSON` hooks.

## Preview bridge

Messages use `{ protocol: 'antifailure-cms', version: 1, session, type, payload }`.
The session is a UUID. `validatePreviewMessage` requires a direction:

- Parent to child: `init`, `update`, `select`.
- Child to parent: `ready`, `select`, `edit`, `error`.

The parent and iframe must independently verify `event.origin`, `event.source`,
and the expected session before handling a message. Payload validation does not
establish those browser identities. Optional properties must be omitted rather
than included as `undefined`, because the protocol accepts JSON values only.

`ready` contains the source-discovered manifest. `init` and `update` carry the
document, a UUID-to-asset-URL map, and `edit` or `preview` mode. Asset URLs permit
HTTP(S) and safe root-relative paths, never data/JavaScript/blob URLs. Empty
`select` payloads clear the current selection. Unsupported protocol versions or
message directions are rejected.

## Verification

```sh
node --test test/*.test.ts
npm run typecheck
```

Tests exercise source updates, custom blocks, reorder cycles, deleted anchors,
collection merges, per-entry malformed reads, prototype pollution, unsafe URLs,
immutable edits, asset discovery, breakpoint isolation, and both bridge
directions.
