import type { Metadata } from "next";
import { AuthScreen } from "@/components/AuthScreen";
import { ChromeProvider } from "@/components/Chrome";

export const metadata: Metadata = {
  title: "Join the waitlist — Antifailure",
  description: "There is no hosted control plane yet. Leave an address and we will tell you when there is.",
};

export default function SignInPage() {
  return (
    <ChromeProvider>
      <AuthScreen mode="signin" />
    </ChromeProvider>
  );
}
