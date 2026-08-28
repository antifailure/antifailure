import NextAuth from "next-auth";
import GitHub from "next-auth/providers/github";
import Google from "next-auth/providers/google";
import MicrosoftEntraID from "next-auth/providers/microsoft-entra-id";
import type { Provider } from "next-auth/providers";

/**
 * Register OAuth apps, then paste IDs/secrets into .env.local (see .env.example).
 * Callbacks: {origin}/api/auth/callback/github | google | microsoft-entra-id
 */
export const configuredProviders = {
  github: Boolean(process.env.AUTH_GITHUB_ID && process.env.AUTH_GITHUB_SECRET),
  google: Boolean(process.env.AUTH_GOOGLE_ID && process.env.AUTH_GOOGLE_SECRET),
  microsoft: Boolean(
    process.env.AUTH_MICROSOFT_ENTRA_ID_ID && process.env.AUTH_MICROSOFT_ENTRA_ID_SECRET,
  ),
};

const providers: Provider[] = [];
if (configuredProviders.github) providers.push(GitHub);
if (configuredProviders.google) providers.push(Google);
if (configuredProviders.microsoft) {
  providers.push(
    MicrosoftEntraID({
      issuer:
        process.env.AUTH_MICROSOFT_ENTRA_ID_ISSUER ??
        "https://login.microsoftonline.com/common/v2.0",
    }),
  );
}

export const { handlers, auth, signIn, signOut } = NextAuth({
  providers,
  trustHost: true,
  pages: {
    signIn: "/signin",
  },
});
