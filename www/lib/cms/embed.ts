/** This policy belongs inside the child document before any user content.
 * The iframe additionally has an opaque origin and no navigation privileges. */
export const EMBED_CSP = "default-src 'none'; script-src https: 'unsafe-inline'; style-src https: 'unsafe-inline'; img-src https: data: blob:; font-src https: data:; connect-src https:; media-src https: blob:; frame-src 'none'; form-action 'none'; base-uri 'none'";

export function embedDocument(html: string, css: string, javascript: string): string {
  // Closing style/script tokens in authored code are ordinary content here.
  // They cannot escape the sandbox, and the first CSP cannot be weakened by
  // later meta tags. Escape the delimiters to preserve literal JS/CSS strings.
  const safeCss = css.replace(/<\/style/gi, "<\\/style");
  const safeScript = javascript.replace(/<\/script/gi, "<\\/script");
  return `<!doctype html><html><head><meta http-equiv="Content-Security-Policy" content="${EMBED_CSP}"><meta name="viewport" content="width=device-width,initial-scale=1"><style>html,body{margin:0;min-height:100%;box-sizing:border-box}*,*::before,*::after{box-sizing:inherit}${safeCss}</style></head><body>${html}<script>${safeScript}</script></body></html>`;
}
