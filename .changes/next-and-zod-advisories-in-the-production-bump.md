# security

The production dependency update moves Next.js to 16.4.3 in the console and
the public site, and zod to 4.6.7 in the control plane's API. Next.js 16.4.0
and earlier carry two critical advisories, GHSA-2464-fhr4-w9xw and
GHSA-673q-mjm6-r5xr, both remote code execution, and one high, a denial of
service through Server Actions. None of the three reached anything we run:
the console and the site are static exports served as files, so there is no
image optimisation endpoint, no cached server function and no Server Action
to call. They are fixed anyway, because a static export is a setting, and a
setting can change.

The zod advisory does reach running code. GHSA-mrhx-c7xc-m3w5 lets an
oversized collection exhaust memory while it is validated against a size
constraint, and the API validates request bodies with zod. Version 4.6.6 is
the first fixed release.

Next.js 16.4.3 rather than 16.4.2 is deliberate. Google Fonts sometimes
serves a font file at a URL with no extension, and every release before
16.4.3 fails the site's build when it does.

Four more packages take the releases that fix advisories published on 22 and
23 October. The public site moves DOMPurify to 3.4.17, which fixes one
critical and one high advisory: GHSA-cg2g-xh64-v4q7, a SAFE_FOR_TEMPLATES
bypass, and GHSA-mrv2-3pqr-j5jr, an IN_PLACE hook leaving a detached subtree
unsanitized, plus a low one. DOMPurify reaches the site only inside the
analytics library, which is not fetched at all for a reader who has opted
out or whose browser sends Global Privacy Control. The control plane's
API moves fast-uri to 3.1.9, which fixes four advisories in how it parses and
normalises URIs; the MCP SDK loads it through its JSON schema validator.
body-parser moves to 2.4.0 for a denial of service through trailing bytes
after a compressed body, and ip-address to 10.7.5 for a zone identifier
matched in quadratic time. Both arrive with Express, which the SDK depends on
and the API never imports. body-parser 2.4.0 requires qs 6.16.1, so qs moves
with it.

The documentation site's build tooling takes the same Next.js 16.4.3 and
undici 8.12.3, the release after the one that fixed three undici advisories,
so the console, the site and the documentation build resolve the same
Next.js release.
