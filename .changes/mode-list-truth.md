# fixed

The site described the egress modes as a set it does not have. The firewall
product page's section title read "Simulate, capture, mock, or deny", where the
schema's modes are `block`, `allow`, `capture`, `mock`, `sandbox` and `synth`:
two of those four words are not modes and four real ones were missing, on a page
in the sitemap. Separately, `synth` had reached the schema and the proxy and
nothing that describes them, so the README, `llms.txt`, the product FAQ, the
route metadata behind the site's JSON-LD and a blog post had each settled on
five. The homepage firewall film labelled its last rule `*:deny`, and the
validator refuses a `*` rule in any mode but `block`, so the label a reader was
most likely to copy could not work. The manifest reference gave `egress.default`
as `block` or `allow` where the schema gives all six.

The manifest reference had the same defect for the same reason. It gave
`egress.default` as `block` or `allow` where the schema gives six, `build.strategy`
as three of four while documenting the fourth key four rows further down, and
`database.provider` as two of four while the engine constructs all four. The
documentation index said every reference page is generated from the thing it
documents and gated against drift, and named that page as one of them. Three of
the four are genuinely checked; that one is written by hand and nothing reads
it, which is why the rows drifted under a claim that they could not. The index
now says which three are checked and points at the generated schema page as the
one to trust.

`tools/modecheck` is the new gate. It reads the schema rather than keeping a
second copy of anything. In prose it fails a document that states a count that
is not the real one, names something that is not a mode alongside things that
are, or promises the whole set and then lists part of it. In the reference
tables it fails a cell that claims to be listing a key's allowed values and
lists them short, matched to the schema by the heading nesting. It does not
check every closed set the schema declares, which is not reliable when the
values are ordinary English, and its output says so. Run against the tree as it
stood, it finds all thirteen.
