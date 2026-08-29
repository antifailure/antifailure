import type { Metadata } from "next";
import { AuthScreen } from "@/components/AuthScreen";
import { ChromeProvider } from "@/components/Chrome";

export const metadata: Metadata = {
  title: "Sign in — Antifailure",
  description:
    "The hosted control plane is invitation only while it is in development. Sign in with GitHub, or join the waitlist.",
};

export default function SignInPage() {
  return (
    <ChromeProvider>
      <AuthScreen mode="signin" />
    </ChromeProvider>
  );
}
