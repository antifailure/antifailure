import { authoredPage, emptyPageBody, isAuthoredPagePath, pageBlockPrefix, pageContentKey, sitePageSlug, type AuthoredPage, type WebsiteDocument } from "@antifailure/website";

function slug(value: string): string {
  return value.toLowerCase().normalize("NFKD").replace(/[\u0300-\u036f]/gu, "").replace(/[^a-z0-9]+/gu, "-").replace(/^-+|-+$/gu, "").slice(0, 70);
}

export function createAuthoredPage(document: WebsiteDocument, existingPaths: readonly string[], input: {
  kind: AuthoredPage["kind"]; title: string; description: string; path?: string; today: string;
}): { document: WebsiteDocument; path: string } {
  const title = input.title.trim();
  const description = input.description.trim();
  const path = input.path?.trim() || `${input.kind === "post" ? "/blog/" : "/"}${slug(title)}`;
  if (!isAuthoredPagePath(path) || (input.kind === "post" && !/^\/blog\/[a-z0-9-]+$/u.test(path)) || (input.kind === "page" && path.startsWith("/blog/"))) {
    throw new Error("Choose a lowercase path such as /guides/releases or /blog/safer-deploys.");
  }
  if (existingPaths.includes(path) || authoredPage(document, path)) throw new Error("That path already belongs to a page. Open it from the list instead.");
  if (!title || !description || title.length > 180 || description.length > 300) throw new Error("Add a title and a short introduction first.");
  const key = (field: string) => pageContentKey(path, field);
  return { path, document: { ...document, pages: [...(document.pages ?? []), { path, kind: input.kind }], fields: {
    ...document.fields, [key("title")]: title, [key("description")]: description,
    [key("summary")]: description, [key("body")]: emptyPageBody(), [key("published")]: input.today,
    ...(input.kind === "post" ? { [key("tags")]: "" } : {}),
  } } };
}

export function removeAuthoredPageDocument(document: WebsiteDocument, path: string): WebsiteDocument {
  if (!authoredPage(document, path)) return document;
  const fieldsPrefix = `page.${sitePageSlug(path)}.`;
  const blockPrefix = pageBlockPrefix(path);
  const root = `page-${sitePageSlug(path)}`;
  const belongs = (key: string) => key === root || key.startsWith(fieldsPrefix) || key.startsWith(blockPrefix);
  const keep = <T,>(rows: Record<string, T>): Record<string, T> => Object.fromEntries(Object.entries(rows).filter(([key]) => !belongs(key)));
  return { ...document, pages: document.pages?.filter((page) => page.path !== path), fields: keep(document.fields), styles: keep(document.styles), collections: keep(document.collections), sections: {
    hidden: document.sections.hidden.filter((id) => !belongs(id)),
    moves: document.sections.moves.filter((row) => !belongs(row.id)),
    custom: document.sections.custom.filter((row) => !belongs(row.id)),
  } };
}
