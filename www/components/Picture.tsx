import { cn } from "@/lib/cn";
import artManifest from "@/assets/hero/art.json";

/**
 * The hero art, served as AVIF with a WebP fallback.
 *
 * This replaces next/image for the background art. next/image is the
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
 * not laziness: all of it is decorative atmosphere behind real content, so an
 * empty alt is what tells a screen reader to skip it. A non-empty alt here
 * would make a screen reader announce eight pieces of scenery.
 */

/**
 * The intrinsic size of each source, from the same file the encoder reads.
 *
 * Neither number can be guessed. `width` and `height` on the <img> are what
 * let the browser reserve the right box before the bytes arrive, and the
 * srcSet descriptors are what it uses to choose between the two encodings, so
 * a wrong one is a layout shift or a needlessly large download. They were
 * hard-coded at 1536x1024 while every source happened to be that size;
 * footer-aurora.png is 1024x768 and would have been described as something it
 * is not. scripts/optimize-images.mjs checks every entry against the file on
 * disk and fails the build when they disagree.
 */
const ART: Record<string, { width: number; height: number }> = artManifest.art;

function intrinsic(stem: string) {
  const meta = ART[`${stem.replace(/^\//, "")}.png`];
  if (!meta) {
    // Reached only by a caller pointing at art that art.json does not describe,
    // which also means the encoder never produced an .avif or .webp for it: the
    // <img> below would 404. Throwing turns a silently broken image into a
    // build failure naming the file.
    throw new Error(
      `<Picture src="/home/${stem}.png"> has no entry in assets/hero/art.json, ` +
        "so nothing encoded it and the page would request a file that does not exist.",
    );
  }
  return meta;
}

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
  const source = intrinsic(stem.replace(/^\/home\//, ""));

  // The encoder resizes withoutEnlargement, so the wide variant of a source
  // narrower than 1536 is the source's own width. Describing it as 1536w would
  // make a browser on a wide viewport download it expecting more pixels than
  // it holds.
  const wide = Math.min(1536, source.width);
  const narrow = Math.min(768, source.width);

  const srcSet = (ext: "avif" | "webp") =>
    narrow === wide
      ? `${stem}.${ext} ${wide}w`
      : `${stem}-768.${ext} ${narrow}w, ${stem}.${ext} ${wide}w`;

  const img = (
    <img
      src={`${stem}.webp`}
      alt={alt}
      // Always present, even under `fill`, so the browser can reserve the box
      // from the aspect ratio and not shift the layout when the bytes land.
      width={fill ? source.width : (rest as { width: number }).width}
      height={fill ? source.height : (rest as { height: number }).height}
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
