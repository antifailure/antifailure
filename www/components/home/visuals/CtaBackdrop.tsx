import { Picture } from "@/components/Picture";

/**
 * The tree-line from the northern-lights still. Cropped to the forest
 * silhouette, not the whole sky, so the panel can fall into the black footer.
 */
export function CtaBackdrop({ className = "" }: { className?: string }) {
  return (
    <div className={className} aria-hidden>
      <Picture
        src="/home/footer-aurora.png"
        alt=""
        fill
        sizes="100vw"
        className="object-cover object-[center_78%] brightness-[0.78] saturate-[0.8]"
      />
      <div className="absolute inset-0 bg-black/22" />
    </div>
  );
}
