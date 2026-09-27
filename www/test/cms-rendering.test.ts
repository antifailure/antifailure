import test from "node:test";
import assert from "node:assert/strict";
import { emptyWebsiteDocument } from "@antifailure/website";
import { parsePreviewConnection } from "../lib/cms/preview-origin";
import { cmsStyles } from "../lib/cms/styles";
import { embedDocument, EMBED_CSP } from "../lib/cms/embed";

const session = "188e5dcb-4c07-41fa-8e87-5adf80d160c3";
const query = (origin: string, nonce = session) => new URLSearchParams({ parentOrigin: origin, session: nonce }).toString();
test("preview accepts the exact editor origin and refuses lookalikes, credentials, paths and missing nonce", () => {
  const trusted = "https://app.antifailure.dev";
  assert.deepEqual(parsePreviewConnection(query(trusted), trusted), { origin: trusted, session });
  for (const origin of ["https://app.antifailure.dev.evil.example", "https://evil.example", "http://app.antifailure.dev", "https://app.antifailure.dev/admin", "https://attacker@app.antifailure.dev", "null", "javascript:alert(1)"]) assert.equal(parsePreviewConnection(query(origin), trusted), null, origin);
  assert.equal(parsePreviewConnection(query(trusted, "predictable"), trusted), null);
  assert.equal(parsePreviewConnection("", trusted), null);
});
test("localhost preview is development-only unless that exact API origin is configured", () => {
  const trusted = "https://app.antifailure.dev";
  assert.equal(parsePreviewConnection(query("http://localhost:4332"), trusted), null);
  assert.equal(parsePreviewConnection(query("http://localhost.evil.example:4332"), trusted, true), null);
  assert.equal(parsePreviewConnection(query("http://localhost:4332"), trusted, true)?.origin, "http://localhost:4332");
  assert.equal(parsePreviewConnection(query("http://127.0.0.1:4332"), trusted, true)?.origin, "http://127.0.0.1:4332");
});
test("device style scopes do not overlap and media framing reaches actual images", () => {
  const document = emptyWebsiteDocument();
  document.styles["hero.heading"] = { desktop: { fontSize: 80 }, mobile: { fontSize: 30 } };
  document.styles["twins.visual"] = { mobile: { imageFit: "cover", focalX: 75, focalY: 20, minHeight: 240 } };
  const css = cmsStyles(document, (id) => `/v1/website/media/${id}`);
  assert.match(css, /@media \(min-width:1024px\)\{[^}]+font-size:80px/);
  assert.match(css, /@media \(max-width:767px\)\{[^}]+font-size:30px/);
  assert.doesNotMatch(css, /max-width:767px[^}]+font-size:80px/);
  assert.match(css, /\[data-cms-key="twins.visual"\]:not\(\[data-cms-action\] \*\)>img/);
  const focus = document.styles["twins.visual"]?.mobile;
  assert.ok(css.includes(`object-position:${focus?.focalX}% ${focus?.focalY}%`));
  assert.match(css, /height:240px/);
});
test("font overrides use authored font names and reject arbitrary CSS selectors or colors", () => {
  const document = emptyWebsiteDocument();
  document.styles.global = { desktop: { fontFamily: "geist", color: "#193e30" } };
  document.styles['x"]{body{display:none}'] = { desktop: { color: "red;display:none" } };
  const css = cmsStyles(document, (id) => `/v1/website/media/${id}`);
  assert.match(css, /font-family:var\(--font-geist-sans\)/);
  assert.match(css, /color:#193e30/);
  assert.doesNotMatch(css, /display:none/);
});
test("custom code receives its restrictive policy before any authored markup", () => {
  const html = '<button id="action">Click</button>';
  const result = embedDocument(html, 'p::after{content:"</style>"}', 'const text="</script>";');
  assert.ok(result.indexOf(EMBED_CSP) < result.indexOf(html));
  assert.match(result, /frame-src 'none'; form-action 'none'; base-uri 'none'/);
  assert.equal((result.match(/<\/script>/gi) ?? []).length, 1);
  assert.equal((result.match(/<\/style>/gi) ?? []).length, 1);
  assert.match(result, /const text="<\\\/script>"/);
});
