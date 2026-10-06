# security

Four npm advisories published on 2026-10-05 reached the lockfiles. The
critical one is in proxy-addr, in the open source web workspace, installed
there because the MCP SDK depends on Express. Nothing in that workspace
imports Express: the api serves MCP through the SDK's web standard transport
on Hono, so the vulnerable code was installed rather than running. The other
three are build tooling: source-map-js in the console, website, docs and
Next.js example, and smol-toml and postcss-selector-parser in the docs. Each
now resolves to its fixed release.

postcss-selector-parser needed an override, because the docs reach it through
postcss-nested 6, which allows only the 6.x line, and no 6.x release is
fixed. The override lifts that one dependency to 7.1.6 and leaves
postcss-nested where it is. The docs built with and without it are identical,
every page and every stylesheet, and the build was shown to fail when that
parser is broken, so the comparison was exercising it.
