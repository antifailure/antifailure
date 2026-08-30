import { cn } from "@/lib/cn";

/**
 * The hero art, served as AVIF with a WebP fallback.
 *
 * This replaces next/image for the seven background images. next/image is the
 * right tool when there is an image server to resize on demand; this site is a
 * static export, so `images.unoptimized` is on and next/image degrades to a
 * plain <img> pointing at whatever it was given. It was being given 1.5-2MB
 * PNGs.
 *
 * So the responsive work moves to build time. scripts/optimize-images.mjs
 * writes each source at 768w and 1536w in both formats, and this component
 * emits the <picture> that lets the browser choose. A browser that supports
 * neither AVIF nor WebP gets the WebP src and renders nothing useful, which is
 * a deliberate trade: WebP has been supported everywhere since 2020, and
 * keeping a 2MB PNG in the deploy to serve a browser that does not exist in
 * this audience costs every real visitor.
 *
 * `alt` is required and every current caller passes "". That is correct and
 * not laziness: all seven are decorative atmosphere behind real content, so an
 * empty alt is what tells a screen reader to skip them. A non-empty alt here
 * would make a screen reader announce nine pieces of scenery.
 */

type Common = {
  /** Path to the original PNG, e.g. "/home/hero-aurora.png". */
  src: string;
  /** "" for decorative art. Required so it cannot be forgotten. */
  alt: string;
  className?: string;
  /** Matches the CSS box the image lands in, so the browser picks a width. */
  sizes?: string;
  /**
   * Only for an image above the fold that is the LCP candidate. Sets
   * fetchpriority=high and disables lazy loading. Exactly one per page.
   */
  priority?: boolean;
};

type Props =
  | (Common & { fill: true; width?: never; height?: never })
  | (Common & { fill?: false; width: number; height: number });

export function Picture({
  src,
  alt,
  className,
  sizes = "100vw",
  priority = false,
  ...rest
}: Props) {
  const stem = src.replace(/^\/?/, "/").replace(/\.png$/, "");
  const fill = "fill" in rest && rest.fill === true;

  const srcSet = (ext: "avif" | "webp") =>
    `${stem}-768.${ext} 768w, ${stem}.${ext} 1536w`;

  const img = (
    <img
      src={`${stem}.webp`}
      alt={alt}
      // Always present, even under `fill`, so the browser can reserve the box
      // from the aspect ratio and not shift the layout when the bytes land.
      // All seven sources are 1536x1024.
      width={fill ? 1536 : (rest as { width: number }).width}
      height={fill ? 1024 : (rest as { height: number }).height}
      sizes={sizes}
      // Preload is a mandatory fetch; fetchPriority tells the browser this one
      // outranks the other images once it has them all.
      fetchPriority={priority ? "high" : undefined}
      loading={priority ? "eager" : "lazy"}
      decoding={priority ? "sync" : "async"}
      className={cn(fill && "absolute inset-0 h-full w-full", className)}
      // Decorative. Empty alt already hides it from the accessibility tree;
      // this covers the assistive technologies that treat alt="" inconsistently.
      aria-hidden={alt === "" ? true : undefined}
    />
  );

  return (
    <picture className={cn(fill && "absolute inset-0")}>
      <source type="image/avif" srcSet={srcSet("avif")} sizes={sizes} />
      <source type="image/webp" srcSet={srcSet("webp")} sizes={sizes} />
      {img}
    </picture>
  );
}
