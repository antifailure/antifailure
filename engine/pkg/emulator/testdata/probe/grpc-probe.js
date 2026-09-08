// Does an unmodified Google gRPC client traverse the sidecar's inspected path?
//
// The sidecar terminates TLS with the environment's authority and sets no
// ALPNProtocols, then reads HTTP/1.1 out of the connection. gRPC is defined
// over HTTP/2 and grpc-js requires h2 to be negotiated. This runs the real
// @google-cloud/pubsub client at a proxy built the same way and records what
// actually happens, rather than asserting it from a code read.
//
// Nothing here overrides an endpoint. PUBSUB_EMULATOR_HOST is deliberately
// NOT set, because setting it is the thing this whole approach exists to
// avoid and it would also switch the client to plaintext, which would answer
// a different question.
const { PubSub } = require('@google-cloud/pubsub');

async function main() {
  const t0 = Date.now();
  const pubsub = new PubSub({ projectId: process.env.GOOGLE_CLOUD_PROJECT });
  const out = { client: '@google-cloud/pubsub',
    version: require('./node_modules/@google-cloud/pubsub/package.json').version };
  try {
    const [topic] = await pubsub.createTopic('af-l33-probe');
    out.ok = true;
    out.topic = topic.name;
  } catch (e) {
    out.ok = false;
    out.ms = Date.now() - t0;
    out.error = String(e.message || e);
    out.code = e.code;
    out.details = e.details;
  }
  console.log(JSON.stringify(out, null, 2));
}
main().then(() => process.exit(0));
