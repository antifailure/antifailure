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

`tools/modecheck` is the new gate. It reads the modes from
`schemas/manifest.v1.json` rather than keeping a second copy, and fails a
document that states a count that is not the real one, names something that is
not a mode alongside things that are, or promises the whole set and then lists
part of it. Run against the tree as it stood, it finds all ten.
