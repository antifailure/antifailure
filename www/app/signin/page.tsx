import { pageMetadata } from "@/lib/seo";
import { AuthScreen } from "@/components/AuthScreen";
import { ChromeProvider } from "@/components/Chrome";

export const metadata = pageMetadata("/signin");

export default function SignInPage() {
  return (
    <ChromeProvider>
      <AuthScreen mode="signin" />
    </ChromeProvider>
  );
}
