# security

The detector that stops a production credential reaching a sandbox could not
see Google Cloud or Azure. It knew seventeen shapes and every one belonged to a
vendor that ships a prefix, which is why AWS was in it and the other two large
clouds were not: their most dangerous credentials do not have one. A GCP service
account key is a JSON file whose private key field holds a PEM block, and there
is nothing in the key material that says Google. An Azure storage account key is
86 characters of base64 and nothing else; what says Azure is the field name
beside it in the connection string. So an environment holding either one could
act on the real project, and the refusal that exists to prevent exactly that let
it through.

It now recognises three Google shapes, the API key, the OAuth access token and
the service account key, and three Azure ones, the storage account key, the
shared access key that Service Bus, Event Hubs, IoT Hub and Relay all sign with,
and the Entra client secret. A shape with no prefix of its own is only a finding
when a marker naming the provider sits near it, because reporting a bare TLS key
as Google's would send somebody to rotate a credential they do not have.

The live versus test rule is unchanged and it now covers a provider that draws
no distinction in the credential itself: Azurite's published development key is
live shaped and is exactly what a sandbox is supposed to hold, so it is told
apart by the account name in the same connection string and is not refused.
