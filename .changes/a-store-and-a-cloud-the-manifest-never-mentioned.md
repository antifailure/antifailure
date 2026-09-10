# added

A compose file running ClickHouse and LocalStack produced a manifest that
mentioned neither.

Both halves were a silence rather than a wrong answer, which is why nothing
caught them. The image classifier recognised ClickHouse, Redis and
Elasticsearch, and the merge read postgres, asked about mysql and mongodb and
dropped every other classification on the floor; Kafka was not classified at
all. So the twin of an analytics product came up holding a masked Postgres and
an empty everything else. af init now proposes a stance for every store beside
the primary, with the reason written per engine, and a store this build has no
provider for is reported with the stance it would have taken instead of being
written into a manifest af up would then refuse.

A cloud emulator in that same compose file is the strongest statement a
repository makes about which cloud it talks to, because somebody configured it
rather than installed it. LocalStack with SERVICES=s3,sqs,sns is read as the
roster, and each service on it gets the catalog's rule for the real host.

The catalog itself matched a dependency by its exact name, which works for a
package called stripe and stops working the moment a vendor ships one client
per service. Twenty two entries are added, covering Lambda, CloudWatch, KMS,
Step Functions, Athena, Bedrock, Cognito, API Gateway, Firehose, BigQuery,
Cloud Logging, Cloud Monitoring, Bigtable, Spanner, Vertex AI, Cloud Run, the
Google and Microsoft token endpoints, Azure OpenAI, Azure AI Search, Azure
Monitor and App Configuration. Every host is written out rather than derived
from the package name, because AWS spells Step Functions as states and
CloudWatch Logs as logs, and a host guessed wrong matches nothing while looking
like coverage.

Measured against the probe corpus, which grew from 30 concrete cloud endpoints
to 62: before, 30 were named by a rule and 32 were refused with "no rule
matches"; after, 62 are named and none is answered by a wildcard. What the
catalog still does not cover is named at af init rather than discovered as an
unreadable refusal in an environment, and the one endpoint that cannot be
covered without reintroducing a wildcard is stated in the entry and held there
by a test.
