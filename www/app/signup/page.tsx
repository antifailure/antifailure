import { auth, configuredProviders } from "@/auth";
import { AuthScreen } from "@/components/AuthScreen";
import { ChromeProvider } from "@/components/Chrome";
import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Sign up — Antifailure",
  description: "Create an Antifailure account.",
};

export default async function SignUpPage({
  searchParams,
}: {
  searchParams: Promise<{ joined?: string; error?: string }>;
}) {
  const session = await auth();
  const params = await searchParams;
  return (
    <ChromeProvider>
      <AuthScreen
        mode="signup"
        configured={configuredProviders}
        sessionEmail={session?.user?.email ?? null}
        oauthError={params.error ? "Social sign-in was cancelled or failed. Try again." : null}
      />
    </ChromeProvider>
  );
}
