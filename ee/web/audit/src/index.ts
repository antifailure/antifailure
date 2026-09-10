// The public surface of the audit streaming package.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

export {
  Queue,
  PermanentError,
  sign,
  verify,
  type Entry,
  type Batch,
  type Manifest,
  type Sink,
  type SinkState,
  type QueueOptions,
  type Clock,
} from './sink.ts'

export {
  SplunkSink,
  EventHubsSink,
  ObjectStoreSink,
  WebhookSink,
  verifyWebhook,
  type Fetcher,
  type SplunkOptions,
  type EventHubsOptions,
  type ObjectStoreOptions,
  type WebhookOptions,
} from './sinks.ts'

export {
  Forwarder,
  startForwarder,
  type ForwarderOptions,
  type ForwarderHandle,
  type Pass,
} from './forwarder.ts'

export {
  fromEnvironment,
  known,
  SinkRefused,
  SinkEnv,
  KeyEnv,
  IntervalEnv,
  BatchEnv,
  DeliveryBatchEnv,
  SplunkUrlEnv,
  SplunkTokenEnv,
  SplunkIndexEnv,
  SplunkSourcetypeEnv,
  EventHubsUrlEnv,
  EventHubsAuthorizationEnv,
  WebhookUrlEnv,
  WebhookSecretEnv,
  DEFAULT_INTERVAL_MS,
  DEFAULT_BATCH,
  type StreamConfig,
} from './configure.ts'
