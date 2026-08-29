import { pageMetadata } from "@/lib/seo";
import { AuthScreen } from "@/components/AuthScreen";
import { ChromeProvider } from "@/components/Chrome";

export const metadata = pageMetadata("/signup");

export default function SignUpPage() {
  return (
    <ChromeProvider>
      <AuthScreen mode="signup" />
    </ChromeProvider>
  );
}
