import type { MetadataRoute } from "next";
import { SITE_DESCRIPTION, SITE_NAME } from "@/lib/site";

/**
 * The web app manifest.
 *
 * Modest but not pointless: it is what stops a phone from labelling a saved
 * shortcut with a truncated <title>, and `theme_color` is what colours the
 * browser chrome on Android instead of leaving it default grey.
 */
export const dynamic = "force-static";

export default function manifest(): MetadataRoute.Manifest {
  return {
    name: SITE_NAME,
    short_name: SITE_NAME,
    description: SITE_DESCRIPTION,
    start_url: "/",
    display: "standalone",
    // Matches the off-white the site actually paints, not #ffffff. A raw white
    // splash behind a near-white page is a visible seam on a phone.
    background_color: "#f7f7f5",
    theme_color: "#101014",
    icons: [
      { src: "/icon.svg", sizes: "any", type: "image/svg+xml", purpose: "any" },
      { src: "/apple-icon.svg", sizes: "180x180", type: "image/svg+xml" },
    ],
  };
}
