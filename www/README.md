# Website

Public marketing site, in-product docs, and sign-in for [antifailure.dev](https://antifailure.dev).

```
cd www
npm install
cp .env.example .env.local
npx auth secret   # writes AUTH_SECRET into .env.local
npm run dev
```

OAuth providers are optional. With no GitHub, Google, or Microsoft credentials, sign-in still renders and the rest of the site works. Callbacks are `{AUTH_URL}/api/auth/callback/{github,google,microsoft-entra-id}`.
