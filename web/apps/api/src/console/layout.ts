// The page shell, and the escaping every page depends on.
//
// Templates are tagged template literals rather than a framework. The rule that
// makes that safe is one line and is enforced by the type system: `html` escapes
// every interpolation, and the only way to insert markup is to wrap it in
// `raw()`, which is a deliberate, greppable act.
//
// Without that rule this file would be an XSS surface, because almost every
// value on these pages came from somewhere else: a branch name, a repository
// name, a client label somebody typed into `af login --client-label`.

import { CONSOLE_CSS } from './styles.ts'

/** Markup that has already been escaped, or is trusted by construction. */
export class Html {
  readonly value: string
  constructor(value: string) {
    this.value = value
  }
  toString(): string {
    return this.value
  }
}

/** Wraps a string as markup. Every use is a place to check by hand. */
export function raw(value: string): Html {
  return new Html(value)
}

export function escape(value: unknown): string {
  if (value === null || value === undefined) return ''
  return String(value)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;')
}

/**
 * The template tag. Everything interpolated is escaped unless it is already
 * Html, and an array is joined so that a list of rows reads naturally.
 */
export function html(strings: TemplateStringsArray, ...values: unknown[]): Html {
  let out = strings[0] ?? ''
  for (let i = 0; i < values.length; i++) {
    const v = values[i]
    if (v instanceof Html) out += v.value
    else if (Array.isArray(v)) out += v.map((x) => (x instanceof Html ? x.value : escape(x))).join('')
    else out += escape(v)
    out += strings[i + 1] ?? ''
  }
  return new Html(out)
}

export interface Viewer {
  /** Needed to attribute an approval. A console that approved a device login
   *  without recording who did it would put an unattributable token in an
   *  audit log whose whole purpose is attribution. */
  userId: string
  label: string
  organization: string | null
  role: string | null
  csrfToken: string
}

export interface PageOptions {
  title: string
  /** Which nav entry is the current page. */
  current?: string
  viewer?: Viewer | null
  /** Shown in the rail, so nobody mistakes staging for production. */
  environmentLabel?: string
  /** A description for the tab and for anything that unfurls a link. */
  description?: string
}

const NAV: { href: string; key: string; label: string; icon: Html; group: string }[] = [
  { group: 'Delivery', href: '/environments', key: 'environments', label: 'Environments', icon: icon('M3 6h14M3 10h14M3 14h9') },
  { group: 'Delivery', href: '/runs', key: 'runs', label: 'Runs', icon: icon('M4 10l4 4 8-8') },
  { group: 'Evidence', href: '/masking', key: 'masking', label: 'Masking', icon: icon('M10 3l6 3v5c0 3.5-2.6 5.9-6 6.9-3.4-1-6-3.4-6-6.9V6z') },
  { group: 'Evidence', href: '/network', key: 'network', label: 'Network', icon: icon('M10 3v14M3 10h14M5 5l10 10M15 5L5 15') },
  { group: 'Evidence', href: '/audit', key: 'audit', label: 'Audit log', icon: icon('M5 3h10v14H5zM8 7h4M8 10h4M8 13h2') },
  { group: 'Organization', href: '/settings/keys', key: 'keys', label: 'Provider keys', icon: icon('M12 3a4 4 0 100 8 4 4 0 000-8zM10 9l-6 6v2h3v-2h2v-2h1') },
  { group: 'Organization', href: '/settings/members', key: 'members', label: 'Members', icon: icon('M7 9a3 3 0 100-6 3 3 0 000 6zM2 17c0-3 2.2-5 5-5s5 2 5 5M14 8a2.5 2.5 0 100-5M13 12c2.6 0 5 1.7 5 5') },
]

function icon(path: string): Html {
  // stroke-width and size are fixed for every icon in the set, because an icon
  // set at five sizes and two weights is the most reliable sign that nobody
  // owned the visual language.
  return raw(
    `<svg width="16" height="16" viewBox="0 0 20 20" fill="none" stroke="currentColor" ` +
      `stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">` +
      `<path d="${path}"/></svg>`,
  )
}

function initials(label: string): string {
  const parts = label.trim().split(/[\s-_]+/).filter(Boolean)
  if (parts.length === 0) return '?'
  if (parts.length === 1) return parts[0]!.slice(0, 2).toUpperCase()
  return (parts[0]![0]! + parts[1]![0]!).toUpperCase()
}

/** A full page with the rail. */
export function page(options: PageOptions, body: Html): Html {
  const groups = [...new Set(NAV.map((n) => n.group))]
  const rail = html`
    <aside class="rail">
      <div class="brand">
        <span class="brand-mark" aria-hidden="true">
          ${raw(
            '<svg width="12" height="12" viewBox="0 0 12 12" fill="none" stroke="#fff" stroke-width="1.7" stroke-linecap="round"><path d="M2 9L6 2l4 7"/><path d="M3.6 7h4.8"/></svg>',
          )}
        </span>
        <span class="brand-name">Antifailure</span>
        ${options.environmentLabel ? html`<span class="brand-env">${options.environmentLabel}</span>` : ''}
      </div>

      <nav class="nav" aria-label="Sections">
        ${groups.map(
          (g) => html`
            <div class="nav-group">${g}</div>
            ${NAV.filter((n) => n.group === g).map(
              (n) => html`
                <a href="${n.href}"${options.current === n.key ? raw(' aria-current="page"') : ''}>
                  ${n.icon}<span>${n.label}</span>
                </a>
              `,
            )}
          `,
        )}
      </nav>

      <div class="rail-foot">
        ${
          options.viewer
            ? html`
                <div class="who">
                  <span class="who-avatar" aria-hidden="true">${initials(options.viewer.label)}</span>
                  <span style="min-width:0">
                    <span class="who-name">${options.viewer.label}</span>
                    <span class="who-org">${options.viewer.organization ?? 'no organization'}</span>
                  </span>
                </div>
              `
            : html`<a class="btn" href="/auth/github" style="width:100%">Sign in</a>`
        }
      </div>
    </aside>
  `

  return document(
    options,
    html`
      <div class="shell">
        ${rail}
        <main id="main">${body}</main>
      </div>
    `,
  )
}

/** A page with no rail: sign-in, device approval, errors. */
export function bare(options: PageOptions, body: Html): Html {
  return document(options, html`<main id="main" class="centred">${body}</main>`)
}

function document(options: PageOptions, body: Html): Html {
  return html`<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>${options.title} — Antifailure</title>
    ${options.description ? html`<meta name="description" content="${options.description}" />` : ''}
    <meta name="color-scheme" content="light dark" />
    <meta name="robots" content="noindex" />
    <link rel="icon" href="/console/icon.svg" type="image/svg+xml" />
    <link rel="stylesheet" href="/console/console.css" />
  </head>
  <body>
    <a class="skip" href="#main">Skip to content</a>
    ${body}
  </body>
</html>`
}

export { CONSOLE_CSS }

/** The mark, as a file so the tab has one that is not a framework default. */
export const CONSOLE_ICON = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32">
  <rect width="32" height="32" rx="7" fill="#0a0a09"/>
  <path d="M8 23L16 8l8 15" fill="none" stroke="#fff" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round"/>
  <path d="M11.2 18.5h9.6" fill="none" stroke="#4ade9e" stroke-width="2.6" stroke-linecap="round"/>
</svg>`

/**
 * An empty state, which is a designed screen rather than a blank panel.
 *
 * Three things, every time: what this is, why it is empty, and the one action
 * that fills it. A blank panel cannot distinguish "nothing has happened yet"
 * from "this failed to load", and the reader assumes the second.
 */
export function empty(title: string, explanation: string, action?: Html): Html {
  return html`
    <div class="empty">
      <div class="empty-mark" aria-hidden="true">
        ${raw(
          '<svg width="18" height="18" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M3 6h14v10H3zM3 9h14"/></svg>',
        )}
      </div>
      <h3>${title}</h3>
      <p>${explanation}</p>
      ${action ?? ''}
    </div>
  `
}

/** A state chip: a colour and a word, never a colour alone. */
export function chip(state: string): Html {
  const tone =
    ['running', 'pass', 'verified', 'ready', 'allow', 'active'].includes(state) ? 'ok'
    : ['failed', 'fail', 'blocked', 'block', 'revoked', 'expired'].includes(state) ? 'bad'
    : ['flaky', 'unverified', 'sleeping', 'queued', 'creating', 'pending'].includes(state) ? 'warn'
    : 'neutral'
  return html`<span class="chip ${tone}">${state}</span>`
}

/** A timestamp a person can read, with the exact value in the title. */
export function when(value: Date | string | null | undefined): Html {
  if (!value) return html`<span class="chip neutral">never</span>`
  const d = value instanceof Date ? value : new Date(value)
  if (Number.isNaN(d.getTime())) return html`<span>${String(value)}</span>`
  const iso = d.toISOString()
  return html`<time datetime="${iso}" title="${iso}">${iso.replace('T', ' ').slice(0, 19)}Z</time>`
}
