// The console's stylesheet, in one file, served with a long cache and a hash.
//
// Server-rendered pages and one stylesheet, rather than a single-page
// application. Three reasons, in order of how much they mattered:
//
//   1. The image has no build step. deploy/docker/control-plane.Dockerfile
//      runs TypeScript directly through Node's type stripping, deliberately, so
//      that the image runs the same code path a developer runs. A bundler would
//      make the shipped console a transpiled approximation of the source, which
//      is exactly what that decision was avoiding.
//   2. Same origin. The session cookie is SameSite=Lax and the CSRF token comes
//      from GET /auth/session; a page served by the same process needs no CORS,
//      no token plumbing into a client, and no second place where a session can
//      be mishandled.
//   3. It is faster. Every page here is one request and about 14 KB.
//
// THE PALETTE IS THE MARKETING SITE'S, not a new one. www/app/globals.css:
// warm paper #f7f7f5, black ink, sage #e4f1eb, and a green accent. The console
// is the same product as the page somebody arrived from, and the fastest way to
// look like a different company is to pick different colours.
//
// The green is darkened to #0a6b45 for anything carrying text. The brand's
// #33bf00 is 2.2:1 on paper, which is fine for a marketing headline sitting at
// 64px and not fine for a 14px label, and shipping the brighter one because it
// is the brand colour is how a console ends up unreadable in daylight.

export const CONSOLE_CSS = `
/* ---------------------------------------------------------------------------
   Tokens. Light is defined on bare :root so that nothing depends on a media
   query having matched; dark redefines only what changes.
   --------------------------------------------------------------------------- */
:root {
  --paper: #f7f7f5;
  --surface: #ffffff;
  --surface-sunk: #f1f1ee;
  --ink: #0a0a09;
  --ink-2: #3f3f3a;
  --ink-3: #6b6b64;
  --hairline: rgba(10, 10, 9, 0.14);
  --hairline-soft: rgba(10, 10, 9, 0.07);

  --sage: #e4f1eb;
  --sage-2: #cae6d9;
  --accent: #0a6b45;
  --accent-ink: #ffffff;
  --accent-wash: #e8f4ee;

  --danger: #9b1c1c;
  --danger-wash: #fdeaea;
  --warn: #7a4a00;
  --warn-wash: #fdf1de;

  --radius: 6px;
  --radius-lg: 10px;
  --rail: 232px;

  --mono: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace;
  --sans: ui-sans-serif, system-ui, -apple-system, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
}

:root:not([data-theme="light"]) {
  color-scheme: light dark;
}

@media (prefers-color-scheme: dark) {
  :root:not([data-theme="light"]) {
    --paper: #0d0e0d;
    --surface: #151716;
    --surface-sunk: #101211;
    --ink: #ecefec;
    --ink-2: #b3b8b4;
    --ink-3: #868d88;
    --hairline: rgba(236, 239, 236, 0.16);
    --hairline-soft: rgba(236, 239, 236, 0.08);

    --sage: #16241d;
    --sage-2: #1d3229;
    --accent: #4ade9e;
    --accent-ink: #06120c;
    --accent-wash: #13251c;

    --danger: #f78a8a;
    --danger-wash: #2a1414;
    --warn: #e8b464;
    --warn-wash: #2a1f0d;
  }
}

:root[data-theme="dark"] {
  --paper: #0d0e0d;
  --surface: #151716;
  --surface-sunk: #101211;
  --ink: #ecefec;
  --ink-2: #b3b8b4;
  --ink-3: #868d88;
  --hairline: rgba(236, 239, 236, 0.16);
  --hairline-soft: rgba(236, 239, 236, 0.08);
  --sage: #16241d;
  --sage-2: #1d3229;
  --accent: #4ade9e;
  --accent-ink: #06120c;
  --accent-wash: #13251c;
  --danger: #f78a8a;
  --danger-wash: #2a1414;
  --warn: #e8b464;
  --warn-wash: #2a1f0d;
}

/* ---------------------------------------------------------------------------
   Base
   --------------------------------------------------------------------------- */
* { box-sizing: border-box; }

html { -webkit-text-size-adjust: 100%; }

body {
  margin: 0;
  /* Explicit, because the viewer paints its own ground behind the page and a
     transparent body borrows it. */
  background: var(--paper);
  color: var(--ink);
  font-family: var(--sans);
  font-size: 15px;
  line-height: 1.55;
  -webkit-font-smoothing: antialiased;
  min-width: 320px;
}

a { color: inherit; text-decoration: none; }
a:hover { text-decoration: underline; text-underline-offset: 3px; }

/* One visible focus ring for everything, and never removed without a
   replacement. Keyboard navigation is the first thing to break in a console
   and the last thing anybody tests. */
:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 2px;
  border-radius: 3px;
}

h1, h2, h3 {
  margin: 0;
  font-weight: 620;
  letter-spacing: -0.021em;
  line-height: 1.15;
  text-wrap: balance;
}
h1 { font-size: 27px; }
h2 { font-size: 18px; letter-spacing: -0.014em; }
h3 { font-size: 15px; letter-spacing: -0.008em; }

p { margin: 0 0 12px; max-width: 68ch; }
p:last-child { margin-bottom: 0; }

code, kbd, .mono { font-family: var(--mono); font-size: 0.92em; }

.skip {
  position: absolute; left: -9999px; top: 0;
  background: var(--ink); color: var(--paper);
  padding: 10px 16px; z-index: 100; border-radius: 0 0 var(--radius) 0;
}
.skip:focus { left: 0; }

/* ---------------------------------------------------------------------------
   Shell
   --------------------------------------------------------------------------- */
.shell { display: grid; grid-template-columns: var(--rail) minmax(0, 1fr); min-height: 100vh; }

.rail {
  border-right: 1px solid var(--hairline);
  background: var(--surface);
  padding: 20px 14px;
  display: flex; flex-direction: column; gap: 22px;
  position: sticky; top: 0; height: 100vh; overflow-y: auto;
}

.brand { display: flex; align-items: center; gap: 9px; padding: 0 8px; }
.brand-mark {
  width: 22px; height: 22px; flex: none;
  border-radius: 5px; background: var(--ink);
  display: grid; place-items: center;
}
.brand-mark svg { display: block; }
.brand-name { font-weight: 600; letter-spacing: -0.022em; font-size: 15px; }
.brand-env {
  font: 500 10px/1 var(--mono);
  letter-spacing: 0.06em; text-transform: uppercase;
  color: var(--ink-3); border: 1px solid var(--hairline);
  padding: 3px 5px; border-radius: 4px;
}

.nav { display: flex; flex-direction: column; gap: 1px; }
.nav-group {
  font: 600 10px/1 var(--sans); letter-spacing: 0.08em; text-transform: uppercase;
  color: var(--ink-3); padding: 14px 8px 6px;
}
.nav a {
  display: flex; align-items: center; gap: 9px;
  padding: 7px 8px; border-radius: var(--radius);
  color: var(--ink-2); font-size: 14px; font-weight: 480;
}
.nav a:hover { background: var(--surface-sunk); color: var(--ink); text-decoration: none; }
.nav a[aria-current="page"] { background: var(--sage); color: var(--ink); font-weight: 560; }
.nav svg { flex: none; opacity: 0.75; }
.nav a[aria-current="page"] svg { opacity: 1; }

.rail-foot { margin-top: auto; border-top: 1px solid var(--hairline-soft); padding-top: 14px; }
.who { display: flex; align-items: center; gap: 9px; padding: 0 8px; }
.who-avatar {
  width: 26px; height: 26px; border-radius: 50%; flex: none;
  background: var(--sage-2); color: var(--ink);
  display: grid; place-items: center;
  font: 600 11px/1 var(--sans);
}
.who-name { font-size: 13px; font-weight: 540; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.who-org { font-size: 11.5px; color: var(--ink-3); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

main { min-width: 0; }
.page { padding: 30px 34px 72px; max-width: 1180px; }

.page-head { margin-bottom: 24px; }
.page-head .eyebrow {
  font: 600 11px/1 var(--sans); letter-spacing: 0.07em; text-transform: uppercase;
  color: var(--ink-3); margin-bottom: 9px;
}
.page-head p { color: var(--ink-2); margin-top: 7px; }

/* ---------------------------------------------------------------------------
   Surfaces. A hairline and a background, not a shadow. Shadows are for things
   that actually float, and in a console almost nothing does.
   --------------------------------------------------------------------------- */
.panel {
  background: var(--surface);
  border: 1px solid var(--hairline);
  border-radius: var(--radius-lg);
  overflow: hidden;
}
.panel + .panel { margin-top: 18px; }
.panel-head {
  display: flex; align-items: center; justify-content: space-between; gap: 14px;
  padding: 13px 16px; border-bottom: 1px solid var(--hairline-soft);
}
.panel-head h2 { font-size: 14px; }
.panel-body { padding: 16px; }

.grid { display: grid; gap: 18px; }
.grid-2 { grid-template-columns: repeat(2, minmax(0, 1fr)); }
.grid-3 { grid-template-columns: repeat(3, minmax(0, 1fr)); }

/* ---------------------------------------------------------------------------
   Tables. Wide content scrolls inside its own container so the page body never
   scrolls sideways.
   --------------------------------------------------------------------------- */
.scroll-x { overflow-x: auto; }
table { border-collapse: collapse; width: 100%; font-size: 13.5px; }
thead th {
  text-align: left; font: 600 11px/1 var(--sans);
  letter-spacing: 0.06em; text-transform: uppercase; color: var(--ink-3);
  padding: 10px 16px; border-bottom: 1px solid var(--hairline-soft);
  white-space: nowrap;
}
tbody td { padding: 11px 16px; border-bottom: 1px solid var(--hairline-soft); vertical-align: middle; }
tbody tr:last-child td { border-bottom: 0; }
tbody tr:hover { background: var(--surface-sunk); }
td.num, th.num { text-align: right; font-variant-numeric: tabular-nums; font-family: var(--mono); }
td.when { color: var(--ink-3); white-space: nowrap; font-variant-numeric: tabular-nums; }

/* ---------------------------------------------------------------------------
   State. A colour and a word, never a colour alone: about one man in twelve
   cannot tell the red one from the green one.
   --------------------------------------------------------------------------- */
.chip {
  display: inline-flex; align-items: center; gap: 6px;
  font: 550 12px/1 var(--sans);
  padding: 4px 8px; border-radius: 5px;
  border: 1px solid var(--hairline-soft);
  background: var(--surface-sunk); color: var(--ink-2);
  white-space: nowrap;
}
.chip::before {
  content: ""; width: 6px; height: 6px; border-radius: 50%;
  background: currentColor; flex: none;
  /* Static. A dot that pulses forever while nothing is happening is noise
     competing with the content it is meant to describe. */
}
.chip.ok      { background: var(--accent-wash); color: var(--accent); border-color: transparent; }
.chip.bad     { background: var(--danger-wash); color: var(--danger); border-color: transparent; }
.chip.warn    { background: var(--warn-wash);   color: var(--warn);   border-color: transparent; }
.chip.neutral { background: var(--surface-sunk); color: var(--ink-3); }

.stat { padding: 15px 16px; }
.stat .k { font: 600 11px/1 var(--sans); letter-spacing: 0.07em; text-transform: uppercase; color: var(--ink-3); }
.stat .v {
  font: 600 25px/1.1 var(--sans); letter-spacing: -0.03em;
  margin-top: 9px; font-variant-numeric: tabular-nums;
}
.stat .sub { font-size: 12.5px; color: var(--ink-3); margin-top: 5px; }

/* ---------------------------------------------------------------------------
   Buttons
   --------------------------------------------------------------------------- */
.btn {
  display: inline-flex; align-items: center; justify-content: center; gap: 7px;
  font: 550 13.5px/1 var(--sans);
  padding: 9px 14px; border-radius: var(--radius);
  border: 1px solid var(--hairline); background: var(--surface); color: var(--ink);
  cursor: pointer; min-height: 36px;
  transition: background 120ms ease, border-color 120ms ease;
}
.btn:hover { background: var(--surface-sunk); text-decoration: none; }
.btn:active { transform: translateY(0.5px); }
.btn-primary { background: var(--accent); color: var(--accent-ink); border-color: transparent; font-weight: 600; }
.btn-primary:hover { background: var(--accent); filter: brightness(1.08); }
.btn-danger { color: var(--danger); border-color: var(--hairline); }
.btn-danger:hover { background: var(--danger-wash); }
.btn[disabled] {
  cursor: not-allowed; opacity: 1;
  background: var(--surface-sunk); color: var(--ink-3); border-color: var(--hairline-soft);
}
.btn-lg { min-height: 44px; padding: 12px 20px; font-size: 15px; }

/* ---------------------------------------------------------------------------
   Forms
   --------------------------------------------------------------------------- */
label { display: block; font-size: 13px; font-weight: 560; margin-bottom: 6px; }
.hint { font-size: 12.5px; color: var(--ink-3); margin-top: 6px; }
input[type="text"], input[type="password"], input[type="number"], select, textarea {
  width: 100%; font: inherit; font-size: 14px;
  padding: 9px 11px; min-height: 38px;
  background: var(--surface); color: var(--ink);
  border: 1px solid var(--hairline); border-radius: var(--radius);
}
input::placeholder { color: var(--ink-3); }
input:hover, select:hover, textarea:hover { border-color: var(--ink-3); }
input[aria-invalid="true"] { border-color: var(--danger); }
.field { margin-bottom: 16px; }
.field:last-child { margin-bottom: 0; }
.row { display: flex; gap: 10px; align-items: flex-end; flex-wrap: wrap; }

/* ---------------------------------------------------------------------------
   Notices. Every one carries a word as well as a colour.
   --------------------------------------------------------------------------- */
.notice {
  display: flex; gap: 11px; align-items: flex-start;
  padding: 12px 14px; border-radius: var(--radius);
  border: 1px solid var(--hairline-soft); background: var(--surface-sunk);
  font-size: 13.5px; margin-bottom: 16px;
}
.notice strong { display: block; margin-bottom: 2px; }
.notice.bad  { background: var(--danger-wash); border-color: transparent; color: var(--danger); }
.notice.ok   { background: var(--accent-wash); border-color: transparent; color: var(--accent); }
.notice.warn { background: var(--warn-wash);   border-color: transparent; color: var(--warn); }
.notice.bad strong, .notice.ok strong, .notice.warn strong { color: inherit; }

/* ---------------------------------------------------------------------------
   Empty states. Built, rather than left as a blank panel.
   A screen with no data and no explanation reads as broken, and the reader
   cannot tell "nothing yet" from "this failed to load".
   --------------------------------------------------------------------------- */
.empty { padding: 44px 24px; text-align: center; }
.empty-mark {
  width: 38px; height: 38px; margin: 0 auto 14px;
  border-radius: 9px; background: var(--sage);
  display: grid; place-items: center; color: var(--accent);
}
.empty h3 { margin-bottom: 6px; }
.empty p { color: var(--ink-3); margin: 0 auto 16px; max-width: 46ch; font-size: 13.5px; }

/* ---------------------------------------------------------------------------
   The device approval screen
   --------------------------------------------------------------------------- */
.centred { min-height: 100vh; display: grid; place-items: center; padding: 24px; }
.card { width: 100%; max-width: 440px; }
.card .panel-body { padding: 26px; }

.code-display {
  font: 620 30px/1 var(--mono);
  letter-spacing: 0.14em;
  /* Letter-spacing adds a space AFTER the last character too, so a centred
     string sits half a space left of true centre. The indent puts it back.
     Invisible until you look for it, and then impossible to unsee. */
  text-indent: 0.14em;
  text-align: center; padding: 20px 12px;
  background: var(--sage); color: var(--ink);
  border-radius: var(--radius); margin: 4px 0 18px;
  /* The dash is part of the code a person read off a terminal, so it is
     rendered rather than being a decorative separator. */
  word-break: break-all;
}
.code-input {
  font: 620 22px/1 var(--mono); letter-spacing: 0.14em; text-align: center;
  text-transform: uppercase; min-height: 52px;
}
.approve-row { display: flex; gap: 10px; margin-top: 18px; }
.approve-row .btn { flex: 1; }

.kv { display: grid; grid-template-columns: auto minmax(0, 1fr); gap: 8px 16px; font-size: 13.5px; }
.kv dt { color: var(--ink-3); white-space: nowrap; }
.kv dd { margin: 0; overflow-wrap: anywhere; }

/* ---------------------------------------------------------------------------
   Mobile. Not an afterthought: the rail becomes a bar and every target grows.
   --------------------------------------------------------------------------- */
@media (max-width: 860px) {
  .shell { grid-template-columns: minmax(0, 1fr); }
  .rail {
    position: static; height: auto; border-right: 0;
    border-bottom: 1px solid var(--hairline);
    padding: 12px 14px; gap: 12px;
  }
  .nav {
    flex-direction: row; gap: 6px;
    overflow-x: auto; scrollbar-width: none; padding-bottom: 2px;
  }
  .nav::-webkit-scrollbar { display: none; }
  .nav-group { display: none; }
  .nav a { white-space: nowrap; padding: 9px 12px; min-height: 40px; }
  .rail-foot { margin-top: 0; border-top: 0; padding-top: 0; }
  .who-name, .who-org { display: none; }
  .page { padding: 20px 16px 56px; }
  h1 { font-size: 23px; }
  .grid-2, .grid-3 { grid-template-columns: minmax(0, 1fr); }
  .code-display { font-size: 24px; letter-spacing: 0.1em; }
}

@media (prefers-reduced-motion: reduce) {
  * { transition: none !important; animation: none !important; }
}
`
