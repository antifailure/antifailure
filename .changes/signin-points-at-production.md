# fixed

"Continue with GitHub" on the marketing site pointed at
`app.dev.antifailure.dev`, the staging control plane, which carries a different
OAuth application and a different database. Every invited person who signed in
from antifailure.dev landed on staging. The origin now lives beside `SITE_URL`
in `www/lib/site.ts`, reads `NEXT_PUBLIC_CONTROL_PLANE_URL`, and falls back to
`https://app.antifailure.dev`, so a build with nothing configured is a
production build.
