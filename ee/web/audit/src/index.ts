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
