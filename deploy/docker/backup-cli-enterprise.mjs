// The operator command line in the enterprise image, copied to backup-cli.mjs.
//
// See backup-cli.mjs for why the path is the same in both images. This copy
// loads the enterprise command line, which registers every table the enterprise
// edition seals values into before handing over to the community one, so
// `node backup-cli.mjs reseal` moves the audit stream's collector credentials
// together with provider keys. The image build asserts that it does.
await import('../ee/web/server/src/backup-cli.ts')
