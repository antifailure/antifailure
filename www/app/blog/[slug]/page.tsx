import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";
import { Container } from "@/components/layout/Container";
import { Breadcrumbs } from "@/components/layout/Breadcrumbs";
import { PageShell } from "@/components/pages/kit";
import { POSTS, POSTS_BY_DATE, formatDate, getPost, postModified } from "@/lib/blog";
import { PageJsonLd, PostJsonLd } from "@/lib/jsonld";
import { OG_IMAGE, SITE_NAME, absoluteUrl, pageTitle } from "@/lib/site";

export function generateStaticParams() {
  return POSTS.map((post) => ({ slug: post.slug }));
}

export async function generateMetadata({
  params,
}: {
  params: Promise<{ slug: string }>;
}): Promise<Metadata> {
  const { slug } = await params;
  const post = getPost(slug);
  if (!post) return {};

  const url = absoluteUrl(`/blog/${post.slug}`);

  return {
    title: { absolute: pageTitle(post.title) },
    description: post.dek,
    alternates: {
      canonical: url,
      types: {
        "text/markdown": `${url}.md`,
        "application/rss+xml": absoluteUrl("/blog/rss.xml"),
      },
    },
    robots: {
      index: true,
      follow: true,
      googleBot: {
        index: true,
        follow: true,
        "max-image-preview": "large",
        "max-snippet": -1,
        "max-video-preview": -1,
      },
    },
    openGraph: {
      // `article`, not `website`. It is what carries the published time into a
      // link preview and tells an aggregator this is a dated piece rather than
      // a page that has always been there.
      type: "article",
      siteName: SITE_NAME,
      locale: "en_US",
      url,
      title: post.title,
      description: post.dek,
      publishedTime: post.published,
      modifiedTime: postModified(post),
      authors: [SITE_NAME],
      tags: [...post.tags],
      images: [{ url: OG_IMAGE.url, width: OG_IMAGE.width, height: OG_IMAGE.height, alt: OG_IMAGE.alt }],
    },
    twitter: {
      card: "summary_large_image",
      title: post.title,
      description: post.dek,
      images: [OG_IMAGE.url],
    },
  };
}

export default async function PostPage({ params }: { params: Promise<{ slug: string }> }) {
  const { slug } = await params;
  const post = getPost(slug);
  if (!post) notFound();

  // The next two posts, so a reader who finished one has somewhere to go and
  // every post has at least two internal links pointing out of it.
  const others = POSTS_BY_DATE.filter((p) => p.slug !== post.slug).slice(0, 2);

  return (
    <PageShell>
      {/* Both, because they describe different things. PostJsonLd is the
          article; PageJsonLd is the page it is served at and the breadcrumb
          trail rendered below, which was visible here and undescribed. */}
      <PageJsonLd path={`/blog/${post.slug}`} />
      <PostJsonLd post={post} />

      <article>
        <header className="pt-28 pb-10 safe-paddings max-lg:pt-16 max-md:pt-12">
          <Container size="1600">
            <Breadcrumbs path={`/blog/${post.slug}`} />
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1 font-mono text-[12px] uppercase tracking-[0.12em] text-gray-new-50">
              <time dateTime={post.published}>{formatDate(post.published)}</time>
              {post.updated ? (
                <>
                  <span aria-hidden="true">·</span>
                  <span>Updated {formatDate(post.updated)}</span>
                </>
              ) : null}
              <span aria-hidden="true">·</span>
              <span>{post.tags.join(", ")}</span>
            </div>
            <h1 className="mt-5 max-w-[1000px] text-[56px] leading-dense tracking-tighter max-xl:text-[46px] max-lg:text-[38px] max-md:text-[30px]">
              {post.title}
            </h1>
            <p className="mt-7 max-w-[680px] text-[20px] leading-snug tracking-extra-tight text-gray-new-40 max-md:text-[17px]">
              {post.dek}
            </p>
          </Container>
        </header>

        {/* `prose-post` is defined in globals.css. The body is authored as real
            semantic HTML rather than a markdown blob, which is what lets an
            answer engine lift a single section cleanly. */}
        <div className="pb-24 safe-paddings max-md:pb-16">
          <Container size="1600">
            <div className="prose-post max-w-[680px]">{post.body}</div>
          </Container>
        </div>
      </article>

      <section className="border-t border-black/12 py-16 safe-paddings max-md:py-12">
        <Container size="1600">
          <h2 className="text-[13px] font-medium uppercase tracking-[0.12em] text-gray-new-50">
            Also here
          </h2>
          <ul className="mt-8 grid grid-cols-2 gap-x-12 gap-y-8 max-md:grid-cols-1">
            {others.map((other) => (
              <li key={other.slug}>
                <Link href={`/blog/${other.slug}`} className="group block">
                  <h3 className="text-[20px] leading-snug tracking-extra-tight text-black group-hover:underline decoration-black/25 underline-offset-4 max-md:text-[18px]">
                    {other.title}
                  </h3>
                  <p className="mt-2 text-[15px] leading-6 tracking-extra-tight text-gray-new-40">
                    {other.dek}
                  </p>
                </Link>
              </li>
            ))}
          </ul>
        </Container>
      </section>
    </PageShell>
  );
}
