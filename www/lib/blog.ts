import type { ReactNode } from "react";
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

export const POSTS: readonly Post[] = [
  MIGRATION_LOCKS,
  MASKING_ATTESTATION,
  EGRESS_MODES,
];

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
