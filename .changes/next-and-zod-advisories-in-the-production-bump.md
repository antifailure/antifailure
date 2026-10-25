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
