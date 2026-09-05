import Link from "next/link";
import { PageHero, PageSection, PageShell } from "@/components/pages/kit";
import { POSTS_BY_DATE, formatDate } from "@/lib/blog";
import { pageMetadata } from "@/lib/seo";

export const metadata = pageMetadata("/blog");

export default function BlogIndexPage() {
  return (
    <PageShell>
      <PageHero
        path="/blog"
        eyebrow="Writing"
        title="Notes on shipping schema changes without taking production down."
        lead="What staging cannot measure, what masking has to prove, and what a test environment should do with an outbound call. Written from the parts of the system that already work."
        actions={null}
      />

      <PageSection>
        {/* An ordered list, newest first, because that is what it is. Each item
            is a real <article> so an extraction pass gets a clean boundary
            between one post and the next rather than one long div. */}
        <ol className="grid gap-y-14 border-t border-black/12 pt-14 max-md:gap-y-10 max-md:pt-10">
          {POSTS_BY_DATE.map((post) => (
            <li key={post.slug}>
              <article>
                <div className="flex items-center gap-x-3 font-mono text-[12px] uppercase tracking-[0.12em] text-gray-new-50">
                  <time dateTime={post.published}>{formatDate(post.published)}</time>
                  <span aria-hidden="true">·</span>
                  <span>{post.tags[0]}</span>
                </div>
                <h2 className="mt-4 max-w-[900px] text-[36px] leading-dense tracking-tighter text-black max-lg:text-[28px] max-md:text-[24px]">
                  <Link prefetch={false} href={`/blog/${post.slug}`} className="hover:underline decoration-black/25 underline-offset-[6px]">
                    {post.title}
                  </Link>
                </h2>
                <p className="mt-4 max-w-[680px] text-[17px] leading-relaxed tracking-extra-tight text-gray-new-40 max-md:text-[15px]">
                  {post.dek}
                </p>
                <Link prefetch={false}
                  href={`/blog/${post.slug}`}
                  className="mt-5 inline-flex items-center gap-x-2 text-[15px] tracking-extra-tight text-black underline decoration-black/20 underline-offset-4 hover:decoration-black"
                >
                  Read it
                  <span aria-hidden="true">→</span>
                </Link>
              </article>
            </li>
          ))}
        </ol>

        <p className="mt-16 border-t border-black/12 pt-8 text-[15px] tracking-extra-tight text-gray-new-40">
          There is a{" "}
          <a
            href="/blog/rss.xml"
            className="text-black underline decoration-black/25 underline-offset-4 hover:decoration-black"
          >
            feed
          </a>
          .
        </p>
      </PageSection>
    </PageShell>
  );
}
