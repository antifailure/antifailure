/**
 * The code block theme.
 *
 * Expressive Code shipped Starlight's defaults here, which meant a code block
 * was painted #f2f2f0 on a #ffffff page: a contrast ratio of 1.12:1 for the
 * fill and 1.12:1 for the border against that fill. Both are below the 1.5:1
 * where an edge starts to be perceptible at all, so a code block had neither a
 * visible fill nor a visible edge. On a phone, where a block is the width of
 * the screen and there is no surrounding layout to imply where it starts, that
 * is the "the codeboxes can barely be seen" report.
 *
 * So code is dark. The product is a command line tool, the marketing site
 * already paints its terminal surfaces dark (`CopyCodeButton`'s terminal
 * variant, the CTA section), and one dark block on the site's #f7f7f5 ground
 * is unmistakably an object rather than a slightly different shade of page.
 *
 * The colours are the site's own tokens, not a bundled theme's. Every one of
 * them is checked against the block background by tools/contrast in the audit
 * that accompanies this change; the floor is 4.5:1 including comments, which
 * is the pair stock dark themes almost always fail.
 */

/** The site's `--color-gray-new-10`. */
const BG = "#18191b";

export const codeTheme = {
  name: "antifailure",
  type: "dark",
  colors: {
    "editor.background": BG,
    "editor.foreground": "#e4e5e7",
    "editor.selectionBackground": "#33bf0033",
    "editorLineNumber.foreground": "#797d86",
    "editorLineNumber.activeForeground": "#c9cbcf",
    "editor.lineHighlightBackground": "#ffffff0a",
    "editorIndentGuide.background1": "#ffffff14",
    "terminal.ansiRed": "#ff9a8c",
    "terminal.ansiGreen": "#7fe05a",
    "terminal.ansiYellow": "#e8c46a",
    "terminal.ansiBlue": "#8fc3ff",
    "terminal.ansiMagenta": "#e0a6f0",
    "terminal.ansiCyan": "#5fd9bc",
  },
  tokenColors: [
    // Comments carry the explanation half of every example on this site, so
    // they are held well above the 4.5:1 floor rather than dimmed to a hint.
    {
      scope: ["comment", "punctuation.definition.comment", "string.comment"],
      settings: { foreground: "#9ea2aa" },
    },
    {
      scope: ["keyword", "storage", "storage.type", "keyword.control", "keyword.operator.new"],
      settings: { foreground: "#7fe05a" },
    },
    {
      scope: ["string", "string.quoted", "punctuation.definition.string", "string.template"],
      settings: { foreground: "#5fd9bc" },
    },
    {
      scope: ["constant.numeric", "constant.language", "constant.character", "constant.other"],
      settings: { foreground: "#e8c46a" },
    },
    // YAML and JSON keys, and object properties. Most of the code on this site
    // is configuration, so this is the scope a reader scans down.
    {
      scope: [
        "entity.name.tag.yaml",
        "support.type.property-name",
        "meta.object-literal.key",
        "variable.other.readwrite.alias",
        "entity.name.tag",
      ],
      settings: { foreground: "#e4e5e7" },
    },
    {
      scope: ["entity.name.function", "support.function", "meta.function-call"],
      settings: { foreground: "#8fc3ff" },
    },
    {
      scope: ["variable", "variable.other", "variable.parameter"],
      settings: { foreground: "#d6d8dc" },
    },
    {
      scope: ["entity.name.type", "support.class", "support.type", "entity.other.inherited-class"],
      settings: { foreground: "#e0a6f0" },
    },
    {
      scope: ["punctuation", "meta.brace", "keyword.operator"],
      settings: { foreground: "#b4b7bd" },
    },
    {
      scope: ["invalid", "invalid.illegal"],
      settings: { foreground: "#ff9a8c" },
    },
    {
      scope: ["markup.inserted", "markup.inserted.diff"],
      settings: { foreground: "#7fe05a" },
    },
    {
      scope: ["markup.deleted", "markup.deleted.diff"],
      settings: { foreground: "#ff9a8c" },
    },
  ],
};
