import { defineCollection } from "astro:content";
import { docsLoader, i18nLoader } from "@astrojs/starlight/loaders";
import { docsSchema, i18nSchema } from "@astrojs/starlight/schema";

export const collections = {
  docs: defineCollection({ loader: docsLoader(), schema: docsSchema() }),
  // The i18n collection exists for one string, not for a second language.
  // Starlight names its sidebar landmark "Main", and the site header this
  // theme puts above it already has a nav named "Main", so from 768px up a
  // screen reader's landmark list held two entries called Main with nothing to
  // tell them apart. Measured rather than assumed: at 900 and 1440 both navs
  // are in the accessibility tree, and at 390 the header's is display:none so
  // only one is. src/content/i18n/en.json renames the sidebar one.
  i18n: defineCollection({ loader: i18nLoader(), schema: i18nSchema() }),
};
