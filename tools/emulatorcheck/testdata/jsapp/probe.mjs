// The application, in JavaScript. Read the imports and the client
// constructions: there is no endpoint here, no AWS_ENDPOINT_URL, no custom
// handler and no proxy agent. This is what the code looks like in production,
// and it is what runs against the emulator.
//
// It is under testdata because it is a fixture of the suite beside it rather
// than a workspace of this repository, and because the install checker walks
// the tree for lockfiles and would otherwise expect CI to install this one the
// way it installs the console.

import { STSClient, GetCallerIdentityCommand } from "@aws-sdk/client-sts";
import {
  S3Client,
  CreateBucketCommand,
  PutObjectCommand,
  GetObjectCommand,
} from "@aws-sdk/client-s3";
import {
  SQSClient,
  CreateQueueCommand,
  SendMessageCommand,
  ReceiveMessageCommand,
} from "@aws-sdk/client-sqs";

const results = [];
const record = (name, detail) => results.push({ name, ok: true, detail });

async function main() {
  // Credentials come from the environment, the way they do in production. The
  // emulator verifies no signature and the sidecar refuses a key that looks
  // live, so what is here is a documented example key and nothing else.
  const sts = new STSClient({});
  const who = await sts.send(new GetCallerIdentityCommand({}));
  record("sts.GetCallerIdentity", who.Arn);

  const bucket = "af-emulatorcheck-js";
  const s3 = new S3Client({});
  await s3.send(new CreateBucketCommand({ Bucket: bucket }));
  await s3.send(
    new PutObjectCommand({
      Bucket: bucket,
      Key: "receipt.txt",
      Body: "the javascript twin wrote this",
    }),
  );
  const got = await s3.send(
    new GetObjectCommand({ Bucket: bucket, Key: "receipt.txt" }),
  );
  const body = await got.Body.transformToString();
  if (body !== "the javascript twin wrote this") {
    throw new Error(`s3 returned ${JSON.stringify(body)}`);
  }
  record("s3.PutObject and GetObject", body);

  const sqs = new SQSClient({});
  const queue = await sqs.send(new CreateQueueCommand({ QueueName: "orders-js" }));
  await sqs.send(
    new SendMessageCommand({ QueueUrl: queue.QueueUrl, MessageBody: "order 41" }),
  );
  const received = await sqs.send(
    new ReceiveMessageCommand({
      QueueUrl: queue.QueueUrl,
      MaxNumberOfMessages: 1,
      WaitTimeSeconds: 2,
    }),
  );
  const message = received.Messages?.[0]?.Body;
  if (message !== "order 41") {
    throw new Error(`sqs returned ${JSON.stringify(message)}`);
  }
  record("sqs.SendMessage and ReceiveMessage", message);

  // The other half. A service outside the declared surface has to be refused,
  // in the shape AWS refuses things in, because an application's error
  // handling is written against that shape.
  const refused = await fetch("https://lambda.us-east-1.amazonaws.com/2015-03-31/functions", {
    method: "GET",
  });
  const text = await refused.text();
  if (refused.status !== 403) {
    throw new Error(`lambda answered ${refused.status}, and it is outside the surface`);
  }
  if (!(refused.headers.get("content-type") || "").includes("xml")) {
    throw new Error(`the refusal was ${refused.headers.get("content-type")}, not AWS's own shape`);
  }
  if (!text.includes("<Code>AccessDenied</Code>")) {
    throw new Error(`the refusal carried no AWS error code: ${text}`);
  }
  record("lambda is refused", `${refused.status} ${refused.headers.get("content-type")}`);

  // And the containment underneath all of it. This container is on the
  // environment's inner network, which Docker creates internal, so a name that
  // is not routed to the sidecar has nowhere to go. The number this proves is
  // zero, and it is a property of the network rather than a promise.
  let escaped = null;
  try {
    const out = await fetch("https://example.com/", {
      signal: AbortSignal.timeout(20000),
    });
    escaped = out.status;
  } catch (err) {
    record("no route out", String(err.cause || err.message || err).slice(0, 120));
  }
  if (escaped !== null) {
    throw new Error(`a request reached the internet and was answered ${escaped}`);
  }
}

main()
  .then(() => {
    console.log("EMULATORCHECK_JS " + JSON.stringify(results));
  })
  .catch((err) => {
    console.log("EMULATORCHECK_JS_FAILED " + (err && err.stack ? err.stack : String(err)));
    process.exitCode = 1;
  });
