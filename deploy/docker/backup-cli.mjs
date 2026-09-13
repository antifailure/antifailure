// The operator command line, by one path that is the same in every image.
//
// The hosted re-sealing job runs `node backup-cli.mjs reseal`, and so does the
// Helm Job the runbook gives. The path is the same in both images on purpose,
// and what it loads is not: this community copy loads the community command
// line, and the enterprise image ships backup-cli-enterprise.mjs under this same
// name, which registers the tables the enterprise edition seals first. A job
// command naming a source path directly would re-seal only what the community
// edition knows about in whichever image it ran in.
await import('./apps/api/src/backup-cli.ts')
