// The public surface of the audit streaming package.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

export {
  Queue,
  PermanentError,
  sign,
  verify,
  manifestKeyFor,
  MANIFEST_KEY_LABEL,
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
  scheduleFromEnvironment,
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
  type Schedule,
} from './configure.ts'

export {
  KINDS,
  isKind,
  checkCustomerDestination,
  read as readDestination,
  delivery as readDelivery,
  save as saveDestination,
  setEnabled as setDestinationEnabled,
  remove as removeDestination,
  sinkFor,
  sealingKeyFrom,
  SealError,
  DestinationRefused,
  type Kind,
  type Destination,
  type Delivery,
  type SaveInput,
  type Route,
} from './destinations.ts'

export {
  auditStreamExtension,
  MAY_CONFIGURE,
  type AuditStreamRoutesOptions,
} from './routes.ts'
