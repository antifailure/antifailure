import { pageMetadata } from "@/lib/seo";
import { AuthScreen } from "@/components/AuthScreen";
import { ChromeProvider } from "@/components/Chrome";

export const metadata = pageMetadata("/signin");

export default function SignInPage() {
  return (
    <ChromeProvider>
      {/* These two render an AuthScreen directly rather than through
          SiteLayout, so they had no <main> at all: no landmark for a screen
          reader, and the skip link in the root layout pointed at an anchor
          that does not exist on them. */}
      <main id="main" tabIndex={-1}>
        <AuthScreen mode="signin" />
      </main>
    </ChromeProvider>
  );
}
