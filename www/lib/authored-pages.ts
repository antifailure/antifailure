import snapshot from "@/lib/cms-snapshot.generated.json";
import { authoredPage, authoredPageContent, normalizeWebsiteDocument, type AuthoredPage } from "@antifailure/website";

const document = normalizeWebsiteDocument(snapshot.document).document;

export function builtAuthoredPages(): AuthoredPage[] { return document.pages ?? []; }
export function builtAuthoredPage(path: string) {
  const page = authoredPage(document, path);
  return page ? { page, content: authoredPageContent(document, page) } : undefined;
}
