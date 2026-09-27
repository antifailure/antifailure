import { createElement, type ReactNode } from "react";
import snapshot from "@/lib/cms-snapshot.generated.json";
import { normalizeWebsiteDocument, pageContentKey, resolveField } from "@antifailure/website";
import { builtAuthoredPage, builtAuthoredPages } from "@/lib/authored-pages";
import { RichText } from "@/lib/cms/richtext";
import { MIGRATION_LOCKS } from "@/content/blog/what-staging-misses";
import { MASKING_ATTESTATION } from "@/content/blog/proving-the-masking-worked";
import { EGRESS_MODES } from "@/content/blog/five-answers-to-an-outbound-call";

/**
 * The blog, at /blog rather than blog.antifailure.dev.
 *
 * That is a deliberate choice and it is the opposite of what most people reach
 * for. A subdomain is commonly treated as a separate site, so a blog on one
 * starts from nothing and earns authority that never reaches the product
 * pages. A subfolder shares the domain outright: the documentation, the
 * product pages and the writing all compound into one thing. For a domain
 * registered this year with almost no external links, that difference is most
 * of the available upside.
 *
 * blog.antifailure.dev still resolves. It sends a 301 here, so the subdomain
 * works for anybody who types it and every link ends up pointing at one
 * canonical URL instead of splitting between two.
 */

export type Post = {
  /** URL segment. Never change one after publishing; add a redirect instead. */
  slug: string;
  /** The <title> and the <h1>. Written as the claim, not the topic. */
  title: string;
  /** Lead paragraph, and the meta description. Under 155 characters. */
  dek: string;
  /** One machine-facing line for llms.txt: what a reader gets from this page. */
  summary: string;
  /** ISO 8601. Becomes datePublished, sitemap lastmod and the RSS pubDate. */
  published: string;
  /** ISO 8601, only when the post is substantively revised. */
  updated?: string;
  tags: string[];
  body: ReactNode;
};

const SOURCE_POSTS: readonly Post[] = [
  MIGRATION_LOCKS,
  MASKING_ATTESTATION,
  EGRESS_MODES,
];

const document = normalizeWebsiteDocument(snapshot.document).document;
const sourcePost = (post: Post): Post => {
  const path = `/blog/${post.slug}`;
  const text = (field: string, fallback: string) => resolveField(document, pageContentKey(path, field), fallback);
  const tags = text("tags", post.tags.join(", ")).split(",").map((tag) => tag.trim()).filter(Boolean);
  return { ...post, title: text("title", post.title), dek: text("description", post.dek),
    summary: text("summary", post.summary), tags: tags.length ? tags : post.tags,
    published: text("published", post.published), updated: text("updated", post.updated ?? "") || undefined };
};

const authoredPosts: Post[] = builtAuthoredPages().filter((page) => page.kind === "post").flatMap((page) => {
  const entry = builtAuthoredPage(page.path);
  if (!entry) return [];
  const content = entry.content;
  return [{ slug: page.path.slice("/blog/".length), title: content.title, dek: content.description,
    summary: content.summary, published: content.published, updated: content.updated || undefined,
    tags: content.tags, body: createElement(RichText, { value: content.body }) }];
});

export const POSTS: readonly Post[] = [...SOURCE_POSTS.map(sourcePost), ...authoredPosts];
if (new Set(POSTS.map((post) => post.slug)).size !== POSTS.length) throw new Error("A CMS article path conflicts with a source article.");

/** Newest first, which is the order the index and the feed both want. */
export const POSTS_BY_DATE = [...POSTS].sort(
  (a, b) => Date.parse(b.published) - Date.parse(a.published),
);

const BY_SLUG = new Map(POSTS.map((p) => [p.slug, p]));

export function getPost(slug: string): Post | undefined {
  return BY_SLUG.get(slug);
}

/** The date a post last meaningfully changed, for sitemap and schema. */
export function postModified(post: Post): string {
  return post.updated ?? post.published;
}

/**
 * A stable, readable date. Fixed to UTC on purpose: `toLocaleDateString` with
 * no timezone renders on the server in the build machine's zone and in the
 * browser in the reader's, and React then complains that the two disagree.
 */
export function formatDate(iso: string): string {
  return new Date(iso).toLocaleDateString("en-US", {
    year: "numeric",
    month: "long",
    day: "numeric",
    timeZone: "UTC",
  });
}
