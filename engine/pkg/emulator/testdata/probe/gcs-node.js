// The vendor's own SDK, unmodified, against the emulated surface.
//
// THE POINT OF THIS FILE IS WHAT IS NOT IN IT. There is no apiEndpoint, no
// STORAGE_EMULATOR_HOST, no baseUrl, no custom transport and no test only
// branch. The client is constructed the way the production code constructs
// it, and the only things that differ are the two an environment sets around
// a process rather than inside it: a proxy to send traffic through and a
// certificate authority to trust. Those are the same two things the sidecar
// already sets for every other host.
const { Storage } = require('@google-cloud/storage');

async function main() {
  const storage = new Storage({ projectId: process.env.GOOGLE_CLOUD_PROJECT });

  const bucketName = 'af-l33-probe';
  const out = { steps: [] };
  const step = async (name, fn) => {
    const t0 = Date.now();
    try {
      const value = await fn();
      out.steps.push({ name, ok: true, ms: Date.now() - t0, value });
    } catch (e) {
      out.steps.push({ name, ok: false, ms: Date.now() - t0, error: String(e.message || e) });
      throw e;
    }
  };

  await step('createBucket', async () => {
    const [b] = await storage.createBucket(bucketName);
    return b.name;
  });
  await step('upload', async () => {
    await storage.bucket(bucketName).file('hello.txt').save('the twin wrote this', {
      resumable: false, contentType: 'text/plain',
    });
    return 'hello.txt';
  });
  await step('download', async () => {
    const [buf] = await storage.bucket(bucketName).file('hello.txt').download();
    return buf.toString();
  });
  await step('list', async () => {
    const [files] = await storage.bucket(bucketName).getFiles();
    return files.map(f => f.name);
  });
  console.log(JSON.stringify(out, null, 2));
}

main().then(() => process.exit(0)).catch(e => {
  console.log(JSON.stringify({ fatal: String(e.message || e) }, null, 2));
  process.exit(1);
});
