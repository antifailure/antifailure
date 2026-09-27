# Website CMS publishing

## Using the editor

Open **Administration → Website** in the operator portal. Website editing is an
owner permission; customer accounts cannot access drafts or publish content.

1. Click text or an image in the homepage preview. The inspector opens its
   content and design settings. Search narrows the fields in a large section.
2. Use **Design** for colors, fonts, sizes, alignment, spacing, and image framing.
   Select desktop, tablet, or mobile before changing its design. Each size keeps
   its own settings so a desktop headline does not crowd a phone.
3. Use the section list to move, hide, or add sections. Text, image, video,
   split layouts, feature groups, calls to action, spacers, shapes, dividers, and code embeds
   can be combined into a new homepage. Custom sections can be duplicated.
4. Open **Header** or **Footer** to edit shared navigation, links, branding, and
   text. These changes apply across the marketing website.
5. Upload images, video, or WOFF2 fonts through **Media library**. Private media
   remains private until referenced by a published version. Files used by
   published history can be archived but cannot be deleted out from under it.
6. Drafts save automatically. **Preview** hides editor controls; **Publish**
   makes the saved content available to visitors. **History** previews an older
   version and restores it as a new publication.

Open **Pages** to browse every built route, including individual articles.
**New page** creates a marketing page at a chosen path; **New article** creates
an entry under `/blog`. Add its title, introduction, summary and body in the
inspector. Articles also have a date and topics. Rich text supports H2/H3
headings, links and lists. Images, video, splits and sandboxed code can be
added as page sections. A new URL is previewable in the editor immediately;
it becomes a public, crawlable page after publication and the static refresh.
The blog index, feed, sitemap and Markdown version include new articles from
that same build. Existing articles remain in the Pages list and keep their
source figures and tables while their copy and metadata can be edited.

Page-wide Ask AI requests start with the entire page selected. Design requests
use a stronger model than short copy changes. A Twins page request to align
small label icons with adjacent text can produce a desktop baseline-alignment edit
that is visible in the preview before it is applied to the draft. Suggestions
still require the editor to accept them and publish.
Ask AI can also propose a new page or Writing article. It checks the proposed
path against built and draft pages, shows the title, introduction and body for
review, and opens the private draft after acceptance. If there is too little
source material for a factual body, the editor must complete the draft before
publication; the publish gate refuses an empty body.

The editor labels customized fields. **Reset** removes the customization and
uses the current code default. A later code change automatically updates fields
that have no override. Customizations retain their values. If two tabs edit the
same draft, the second tab must resolve the conflict instead of silently
overwriting the first tab's work.

Uploads support PNG, JPEG, WebP, and GIF images up to 12 MiB, MP4 and WebM videos
up to 64 MiB, and WOFF2 fonts up to 4 MiB. The library has a 256 MiB total limit.
SVG and HTML uploads are refused; authored shapes are rendered by the site.

The **Code** block accepts HTML, CSS, and JavaScript in separate tabs. Its preview
updates as code changes. Code runs in a sandboxed frame with an opaque origin;
it cannot read the admin page, its credentials, or the surrounding website DOM.
HTTPS scripts, images, fonts, media, and network requests can be used inside the
frame. Forms, nested frames, popups, and top-level navigation are disabled. This
is an interactive embed, not a site-wide tracking-script injector.

Design settings include normal page flow, an overlay, or a fixed position, plus
a layer level and horizontal/vertical offsets. These apply to images, shapes,
code blocks, and other sections. Review each device size when positioning an
overlay; a desktop position does not automatically carry into the phone layout.

Publishing updates the public document immediately. A separate background job
rebuilds static HTML and search-readable copies. The editor reports that refresh
separately and offers a retry if it fails. Local and staging installations never
dispatch a production website refresh.

The public renderer receives the same document in the static build and at runtime.
Source defaults remain in the components; the document stores explicit overrides.
This keeps unedited content following future code changes without overwriting
editor customizations.

## Static build

`www/scripts/cms-snapshot.mjs` runs before development, production builds, and
typechecking. With no configuration it writes the empty source document. It never
reuses a previous generated snapshot. Both generated files are ignored by Git:

- `www/lib/cms-snapshot.generated.json` contains the validated document, revision,
  and SHA-256 content hash.
- `www/public/cms-version.json` contains only the revision and content hash.

`CMS_PUBLISHED_URL` explicitly selects the public published-document endpoint.
`CMS_SNAPSHOT_FILE` selects a local test fixture instead. Setting both is an error.
Neither source uses admin credentials. The content hash is SHA-256 over UTF-8
`stableStringify(validatedDocument)` from `@antifailure/website`.

The fetch has a 15-second deadline covering the response body and a bounded stream
reader. Invalid JSON, invalid individual document entries, a hash mismatch,
redirects, network failures, and non-200 responses stop the production build. A
failed build does not silently substitute source defaults. Runtime tolerance for
individual stale overrides is separate from this publication-integrity check.

The standard Next export renders the document into HTML. Markdown twins are
generated from that rendered HTML afterward, so they describe the same content.

## Refresh workflow

The existing Deploy workflow accepts `cms_revision` and `cms_content_hash` as
informational dispatch inputs. It always fetches the latest published document at
build time. Inputs are never interpolated into shell commands or used to select an
older document.

Immediately before uploading, the workflow fetches the published document again.
A newer revision ends that run without an upload. A rollback or two hashes for
one revision fails the run. Deployments remain serialized and are never cancelled
in the middle of an upload. After upload, a bounded check requires the live
`/cms-version.json` to match both the built revision and hash. An HTTP 200 by itself
does not establish that content was deployed.

Publication can occur after the last pre-upload check. The API and static hosting
provider do not share a transaction. In that window the runtime renderer reads
the current published document, while the newer refresh job updates static HTML.
The refresh worker must only mark a revision deployed after observing its exact
live marker; dispatch acceptance is not deployment completion.

## First rollout

The repository's main branch publishes the marketing site before a new control
plane tag is promoted. The Deploy workflow first reads the published CMS endpoint.
If the production API returns its own 404 and the live site has no CMS marker,
the site job waits while the existing site remains live. Other failures stop the
workflow. After the production control plane is promoted, run Deploy again on
main. The API seeds a valid revision-zero document, so the first website build
uses the source design without an editor publication. No build silently copies
a draft or substitutes code defaults for a previously deployed CMS revision.

## Verification

`npm test` in `www` includes the snapshot suite. It exercises malformed overrides,
hash integrity, stream limits, a real hanging HTTP response, fail-closed errors,
bootstrap ordering, stale-build detection, ordinary-build reset, fixture loading,
and exact live-marker observation. It does not publish a site or require a live
database.
