import { pageMetadata } from "@/lib/seo";
import { PageJsonLd } from "@/lib/jsonld";
import { AuthScreen } from "@/components/AuthScreen";
import { ChromeProvider } from "@/components/Chrome";

export const metadata = pageMetadata("/signup");

export default function SignUpPage() {
  return (
    <ChromeProvider>
      {/* The structured data every other indexable page gets from PageShell.
          This route renders an AuthScreen full bleed instead, so it has no
          shell to inherit it from, and it was excluded from the index for as
          long as it was a waitlist with nothing to rank for. It describes
          creating an account now, on a product anybody can create one on, so it
          is indexed and it needs the same WebPage node and breadcrumb trail as
          everything else.

          The trail is not markup with nothing behind it: AuthScreen renders a
          Home link at the top left, which is the one visible step this
          describes. */}
      <PageJsonLd path="/signup" />
      {/* These two render an AuthScreen directly rather than through
          SiteLayout, so they had no <main> at all: no landmark for a screen
          reader, and the skip link in the root layout pointed at an anchor
          that does not exist on them. */}
      <main id="main" tabIndex={-1}>
        <AuthScreen mode="signup" />
      </main>
    </ChromeProvider>
  );
}
