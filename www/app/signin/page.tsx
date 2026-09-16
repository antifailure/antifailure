import { pageMetadata } from "@/lib/seo";
import { AuthScreen } from "@/components/AuthScreen";
import { ChromeProvider } from "@/components/Chrome";

export const metadata = pageMetadata("/signin");

export default function SignInPage() {
  return (
    <ChromeProvider>
      {/* This renders an AuthScreen directly rather than through SiteLayout,
          so it had no <main> at all: no landmark for a screen reader, and the
          skip link in the root layout pointed at an anchor that does not exist
          on it. */}
      <main id="main" tabIndex={-1}>
        <AuthScreen />
      </main>
    </ChromeProvider>
  );
}
