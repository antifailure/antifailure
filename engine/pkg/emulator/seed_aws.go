package emulator

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// What an AWS declaration becomes inside LocalStack.
//
// EVERY REQUEST BELOW WAS MEASURED against the digest this build pins, on
// 2026-09-20, before it was written. That is not a formality: three of the
// shapes an SDK's documentation would suggest do not work against this
// container, and each one would have failed in a way that reads as a broken
// twin rather than as a wrong request.
//
//   - PutBucketVersioning needs Content-Type: application/xml. Without it the
//     container answers 400 and the bucket stays unversioned, so a run would
//     have reported the bucket reproduced and versioning absent with no
//     explanation anybody could act on.
//   - CreateSecret needs a ClientRequestToken. Real Secrets Manager generates
//     one when the caller does not, and this container answers 400
//     InvalidRequestException instead. The token here is derived from the
//     secret's name so that two runs of one declaration send the same request.
//   - SQS answers the JSON protocol and the queue URL it returns names the
//     host the request arrived on, because this build sets
//     SQS_ENDPOINT_STRATEGY=dynamic. GetQueueAttributes takes that URL rather
//     than the queue's name, which is why the plan captures it rather than
//     composing it: composing it would mean writing LocalStack's own account
//     number into this file, and it would be wrong the day somebody registers
//     a different AWS emulator.
//
// THE REGION IS A HOSTNAME HERE AND A PROPERTY IN PRODUCTION. Every service
// below is addressed at its regional hostname, so a declaration's region
// decides which host the request goes to and therefore which egress rule has
// to route it. It is NOT a property the emulator holds: one LocalStack answers
// for every region at once. Consuming the key is correct because the request
// carries it; believing the twin is therefore regional would not be, and
// nothing here says that.

// awsRegionAttr is the key an AWS declaration carries its region under.
const awsRegionAttr = "region"

// awsDefaultRegion is what a declaration with no region is addressed at.
//
// us-east-1 rather than a refusal, because a declaration whose region comes
// from a provider block is extremely common and the alternative would be a
// twin with no bucket in it over a key the reader never wrote.
const awsDefaultRegion = "us-east-1"

func init() {
	registerSeed("aws_s3_bucket", planS3Bucket)
	registerSeed("aws_sqs_queue", planSQSQueue)
	registerSeed("aws_sns_topic", planSNSTopic)
	registerSeed("aws_dynamodb_table", planDynamoTable)
	registerSeed("aws_kinesis_stream", planKinesisStream)
	registerSeed("aws_ssm_parameter", planSSMParameter)
	registerSeed("aws_secretsmanager_secret", planSecret)
	registerSeed("aws_cloudwatch_event_bus", planEventBus)
}

// awsRegion reads the declaration's region.
func awsRegion(d provider.CloudResource) string {
	if r, ok := d.Attr(awsRegionAttr); ok && strings.TrimSpace(r) != "" {
		return strings.TrimSpace(r)
	}
	return awsDefaultRegion
}

// awsJSON is the header set for one of the AWS JSON protocols.
func awsJSON(target, version string) map[string]string {
	return map[string]string{
		"Content-Type": "application/x-amz-json-" + version,
		"X-Amz-Target": target,
	}
}

// awsQuery is the header set for the AWS query protocol, which SNS still uses.
func awsQuery() map[string]string {
	return map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
}

// planS3Bucket creates a bucket, in path style.
//
// Path style rather than virtual hosted, and the choice is about ROUTING
// rather than about S3. A bucket in the hostname needs the environment to
// route *.s3.*.amazonaws.com, and a manifest that routes only the apex would
// then have its bucket refused while the application, which may address either
// way, works. The apex is the spelling every environment that emulates S3 at
// all has to route, so it is the one that reproduces the most buckets.
func planS3Bucket(d provider.CloudResource) Step {
	region := awsRegion(d)
	step := Step{
		Emulator: AWSName,
		Kind:     "bucket",
		Hosts:    []string{"s3." + region + ".amazonaws.com", "s3.amazonaws.com"},
		Consumed: []string{awsRegionAttr},
		Reasons: map[string]string{
			"lifecycle_rule": "LocalStack stores a lifecycle configuration and never expires " +
				"an object, so a rule reproduced here would be a rule that does nothing",
			"server_side_encryption_configuration": "the AWS surface in this build does not " +
				"answer for KMS, so the key this bucket is encrypted with does not exist in " +
				"the twin",
			"replication_configuration": "there is one emulator and no second region to " +
				"replicate to",
			"logging": "the bucket that would receive the access log is not declared here",
		},
		Actions: []Action{{
			Name: "the bucket",
			Create: []Request{{
				Method: "PUT",
				Path:   "/" + d.Name,
			}},
			Verify: Request{Method: "HEAD", Path: "/" + d.Name},
		}},
	}
	if versioningEnabled(d) {
		step.Consumed = append(step.Consumed, "versioning")
		step.Actions = append(step.Actions, Action{
			Name: "versioning",
			Create: []Request{{
				Method: "PUT",
				Path:   "/" + d.Name,
				Query:  "versioning",
				// Measured: without this header the container answers 400 and
				// the bucket is silently left unversioned.
				Header: map[string]string{"Content-Type": "application/xml"},
				Body: `<VersioningConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/">` +
					`<Status>Enabled</Status></VersioningConfiguration>`,
			}},
			Verify: Request{Method: "GET", Path: "/" + d.Name, Query: "versioning"},
			Expect: "<Status>Enabled</Status>",
		})
	}
	return step
}

// versioningEnabled reads the several spellings a declaration uses for it.
//
// Terraform's aws_s3_bucket_versioning carries status = "Enabled", the older
// inline block carried enabled = true, and a reader of either writes the value
// through unchanged. Both are accepted because refusing one would report
// versioning unreproduced for a bucket that declares it in the spelling this
// happened not to check.
func versioningEnabled(d provider.CloudResource) bool {
	v, ok := d.Attr("versioning")
	if !ok {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "enabled", "true":
		return true
	}
	return false
}

// sqsQueueAttributes are the declaration's keys that CreateQueue takes, and
// the names SQS itself gives them.
//
// One map rather than one per loop below, because the two loops have to agree:
// the first puts the value into the create and the second reads it back off
// the queue, and a key present in one and absent from the other would be a
// value sent and never proved, or an attribute proved that nothing set.
var sqsQueueAttributes = map[string]string{
	"visibility_timeout_seconds": "VisibilityTimeout",
	"message_retention_seconds":  "MessageRetentionPeriod",
	"delay_seconds":              "DelaySeconds",
	"max_message_size":           "MaximumMessageSize",
	"receive_wait_time_seconds":  "ReceiveMessageWaitTimeSeconds",
}

// planSQSQueue creates a queue and the attributes the emulator holds.
func planSQSQueue(d provider.CloudResource) Step {
	region := awsRegion(d)
	host := "sqs." + region + ".amazonaws.com"
	name := d.Name
	consumed := []string{awsRegionAttr}
	attributes := map[string]string{}

	// A FIFO queue is a queue whose name ends in .fifo AND whose FifoQueue
	// attribute is true. The emulator refuses the pair with either half
	// missing, exactly as AWS does, so the name is corrected here rather than
	// left to fail: a declaration carrying fifo_queue = true and a name
	// without the suffix is a declaration Terraform itself would refuse.
	if truthy(d, "fifo_queue") {
		consumed = append(consumed, "fifo_queue")
		attributes["FifoQueue"] = "true"
		if !strings.HasSuffix(name, ".fifo") {
			name += ".fifo"
		}
	}
	for _, key := range sortedKeys(sqsQueueAttributes) {
		if v, ok := d.Attr(key); ok && strings.TrimSpace(v) != "" {
			consumed = append(consumed, key)
			attributes[sqsQueueAttributes[key]] = strings.TrimSpace(v)
		}
	}

	step := Step{
		Emulator: AWSName,
		Kind:     "queue",
		Hosts:    []string{host},
		Consumed: consumed,
		Reasons: map[string]string{
			"kms_master_key_id": "the AWS surface in this build does not answer for KMS, so " +
				"the key this queue is encrypted with does not exist in the twin",
			"redrive_policy": "the dead letter queue it names is a separate declaration, and " +
				"wiring one queue to another is not something this build does yet",
		},
		Actions: []Action{{
			Name: "the queue",
			Create: []Request{{
				Method:  "POST",
				Path:    "/",
				Header:  awsJSON("AmazonSQS.CreateQueue", "1.0"),
				Body:    fmt.Sprintf(`{"QueueName":%q%s}`, name, attributesJSON(attributes)),
				Capture: map[string]string{"QueueUrl": "queueURL"},
			}},
			Verify: Request{
				Method:  "POST",
				Path:    "/",
				Header:  awsJSON("AmazonSQS.GetQueueUrl", "1.0"),
				Body:    fmt.Sprintf(`{"QueueName":%q}`, name),
				Capture: map[string]string{"QueueUrl": "queueURL"},
			},
			Expect: `"QueueUrl"`,
		}},
	}
	// Each attribute proved separately, by reading it back off the queue. A
	// single action covering the queue and everything asked of it would report
	// one verdict for several facts, and the one that mattered would be the
	// one it hid.
	for _, key := range sortedKeys(sqsQueueAttributes) {
		attr := sqsQueueAttributes[key]
		v, ok := attributes[attr]
		if !ok {
			continue
		}
		step.Actions = append(step.Actions, Action{
			Name: key,
			Verify: Request{
				Method: "POST",
				Path:   "/",
				Header: awsJSON("AmazonSQS.GetQueueAttributes", "1.0"),
				Body:   `{"QueueUrl":"{{queueURL}}","AttributeNames":["` + attr + `"]}`,
			},
			Expect: fmt.Sprintf("%q: %q", attr, v),
		})
	}
	if attributes["FifoQueue"] == "true" {
		step.Actions = append(step.Actions, Action{
			Name: "fifo_queue",
			Verify: Request{
				Method: "POST",
				Path:   "/",
				Header: awsJSON("AmazonSQS.GetQueueAttributes", "1.0"),
				Body:   `{"QueueUrl":"{{queueURL}}","AttributeNames":["FifoQueue"]}`,
			},
			Expect: `"FifoQueue": "true"`,
		})
	}
	return step
}

// attributesJSON renders the Attributes member of a CreateQueue request.
func attributesJSON(attributes map[string]string) string {
	if len(attributes) == 0 {
		return ""
	}
	pairs := make([]string, 0, len(attributes))
	for _, k := range sortedKeys(attributes) {
		pairs = append(pairs, fmt.Sprintf("%q:%q", k, attributes[k]))
	}
	return `,"Attributes":{` + strings.Join(pairs, ",") + `}`
}

// planSNSTopic creates a topic.
//
// Verified through ListTopics rather than GetTopicAttributes, because the
// attributes call takes the topic's ARN and the ARN carries the emulator's own
// account number. The expectation is the ARN's last segment with the closing
// tag against it, so a topic called `orders` is not matched by one called
// `orders-dead-letter`.
func planSNSTopic(d provider.CloudResource) Step {
	region := awsRegion(d)
	name := d.Name
	form := "Action=CreateTopic&Version=2010-03-31&Name=" + name
	consumed := []string{awsRegionAttr}
	if truthy(d, "fifo_topic") {
		consumed = append(consumed, "fifo_topic")
		if !strings.HasSuffix(name, ".fifo") {
			name += ".fifo"
		}
		form = "Action=CreateTopic&Version=2010-03-31&Name=" + name +
			"&Attributes.entry.1.key=FifoTopic&Attributes.entry.1.value=true"
	}
	return Step{
		Emulator: AWSName,
		Kind:     "topic",
		Hosts:    []string{"sns." + region + ".amazonaws.com"},
		Consumed: consumed,
		Reasons: map[string]string{
			"kms_master_key_id": "the AWS surface in this build does not answer for KMS, so " +
				"the key this topic is encrypted with does not exist in the twin",
			"delivery_policy": "a delivery policy is about retrying a real endpoint, and an " +
				"emulated topic has no subscriber outside the environment to retry",
		},
		Actions: []Action{{
			Name: "the topic",
			Create: []Request{{
				Method: "POST",
				Path:   "/",
				Header: awsQuery(),
				Body:   form,
			}},
			Verify: Request{
				Method: "POST",
				Path:   "/",
				Header: awsQuery(),
				Body:   "Action=ListTopics&Version=2010-03-31",
			},
			Expect: ":" + name + "</TopicArn>",
		}},
	}
}

// planDynamoTable creates a table, which is the one kind here that cannot be
// created from its name alone.
//
// DynamoDB refuses a table with no key schema, so a declaration carrying no
// hash key produces no request at all and one unmeasured outcome saying so.
// Sending the create anyway would put the emulator's refusal in front of
// somebody as though the twin were broken, when what is missing is in the
// declaration this was handed.
func planDynamoTable(d provider.CloudResource) Step {
	region := awsRegion(d)
	step := Step{
		Emulator: AWSName,
		Kind:     "table",
		Hosts:    []string{"dynamodb." + region + ".amazonaws.com"},
		Consumed: []string{awsRegionAttr, "hash_key", "hash_key_type", "range_key", "range_key_type"},
		Reasons: map[string]string{
			"global_secondary_index": "this build creates the table and its key schema, and " +
				"not its secondary indexes, so a query against one would not find them",
			"local_secondary_index": "this build creates the table and its key schema, and " +
				"not its secondary indexes, so a query against one would not find them",
			"ttl": "LocalStack stores the time to live setting and does not expire an item, " +
				"so an item's age has no effect in the twin",
			"stream_enabled": "the stream would exist and nothing in the twin consumes it",
		},
	}
	hash, ok := d.Attr("hash_key")
	if !ok || strings.TrimSpace(hash) == "" {
		step.Outcomes = append(step.Outcomes, Outcome{
			Name:  d.Name,
			State: Unmeasured,
			Reason: "the declaration carries no hash key, and DynamoDB refuses a table " +
				"without one, so nothing was sent and the twin has no table of this name",
		})
		return step
	}
	definitions := []string{attributeDefinition(hash, attrType(d, "hash_key_type"))}
	schema := []string{fmt.Sprintf(`{"AttributeName":%q,"KeyType":"HASH"}`, hash)}
	if rng, ok := d.Attr("range_key"); ok && strings.TrimSpace(rng) != "" {
		definitions = append(definitions, attributeDefinition(rng, attrType(d, "range_key_type")))
		schema = append(schema, fmt.Sprintf(`{"AttributeName":%q,"KeyType":"RANGE"}`, rng))
	}
	body := fmt.Sprintf(
		`{"TableName":%q,"AttributeDefinitions":[%s],"KeySchema":[%s],"BillingMode":"PAY_PER_REQUEST"}`,
		d.Name, strings.Join(definitions, ","), strings.Join(schema, ","))

	step.Actions = []Action{{
		Name:   "the table",
		Create: []Request{{Method: "POST", Path: "/", Header: awsJSON("DynamoDB_20120810.CreateTable", "1.0"), Body: body}},
		Verify: Request{
			Method: "POST", Path: "/",
			Header: awsJSON("DynamoDB_20120810.DescribeTable", "1.0"),
			Body:   fmt.Sprintf(`{"TableName":%q}`, d.Name),
		},
		Expect: fmt.Sprintf(`"TableName": %q`, d.Name),
	}, {
		// Separately, because a table with the right name and the wrong key is
		// a table every read against it fails on, and one verdict covering
		// both would have reported it present.
		Name: "the key schema",
		Verify: Request{
			Method: "POST", Path: "/",
			Header: awsJSON("DynamoDB_20120810.DescribeTable", "1.0"),
			Body:   fmt.Sprintf(`{"TableName":%q}`, d.Name),
		},
		Expect: fmt.Sprintf(`"AttributeName": %q`, hash),
	}}
	if rng, ok := d.Attr("range_key"); ok && strings.TrimSpace(rng) != "" {
		// Its own action for the same reason the hash key has one: a table
		// with the right partition key and no sort key answers a query with
		// ValidationException, and one verdict covering both would have
		// reported the schema present.
		step.Actions = append(step.Actions, Action{
			Name: "the range key",
			Verify: Request{
				Method: "POST", Path: "/",
				Header: awsJSON("DynamoDB_20120810.DescribeTable", "1.0"),
				Body:   fmt.Sprintf(`{"TableName":%q}`, d.Name),
			},
			Expect: fmt.Sprintf(`"AttributeName": %q`, strings.TrimSpace(rng)),
		})
	}
	// Billing mode is consumed only when the declaration asked for the one
	// this sends. Anything else is reported, because a table declared with
	// provisioned throughput is created here on demand and a reader comparing
	// a capacity alarm against the twin would be comparing against a table
	// that cannot have one.
	if mode, ok := d.Attr("billing_mode"); ok && strings.EqualFold(strings.TrimSpace(mode), "PAY_PER_REQUEST") {
		step.Consumed = append(step.Consumed, "billing_mode")
	} else if ok {
		step.Reasons["billing_mode"] = "the table is created on demand, because throughput " +
			"is a cost and a limit in production and neither exists in an emulator"
	}
	return step
}

// attrType is a key's declared type, defaulting to a string.
func attrType(d provider.CloudResource, key string) string {
	if v, ok := d.Attr(key); ok {
		switch strings.ToUpper(strings.TrimSpace(v)) {
		case "N", "B":
			return strings.ToUpper(strings.TrimSpace(v))
		}
	}
	return "S"
}

func attributeDefinition(name, kind string) string {
	return fmt.Sprintf(`{"AttributeName":%q,"AttributeType":%q}`, name, kind)
}

// planKinesisStream creates a stream.
func planKinesisStream(d provider.CloudResource) Step {
	region := awsRegion(d)
	shards := "1"
	consumed := []string{awsRegionAttr}
	if v, ok := d.Attr("shard_count"); ok && strings.TrimSpace(v) != "" {
		shards = strings.TrimSpace(v)
		consumed = append(consumed, "shard_count")
	}
	return Step{
		Emulator: AWSName,
		Kind:     "stream",
		Hosts:    []string{"kinesis." + region + ".amazonaws.com"},
		Consumed: consumed,
		Reasons: map[string]string{
			"retention_period": "the emulator holds records for its own default and a twin " +
				"is not up long enough for a retention period to mean anything",
			"encryption_type": "the AWS surface in this build does not answer for KMS, so " +
				"the key this stream is encrypted with does not exist in the twin",
		},
		Actions: []Action{{
			Name: "the stream",
			Create: []Request{{
				Method: "POST", Path: "/",
				Header: awsJSON("Kinesis_20131202.CreateStream", "1.1"),
				Body:   fmt.Sprintf(`{"StreamName":%q,"ShardCount":%s}`, d.Name, shards),
			}},
			Verify: Request{
				Method: "POST", Path: "/",
				Header: awsJSON("Kinesis_20131202.DescribeStreamSummary", "1.1"),
				Body:   fmt.Sprintf(`{"StreamName":%q}`, d.Name),
			},
			Expect: fmt.Sprintf(`"StreamName": %q`, d.Name),
		}},
	}
}

// planSSMParameter creates a parameter, and its VALUE is a placeholder.
//
// The value is the whole point of the honesty here. A parameter store entry in
// production holds a connection string, a key, an endpoint: production's own
// configuration. Copying it into a twin would be copying production's secrets
// into a container a third party image runs, which is the one thing this
// product must never do. So the parameter exists under its own name, the
// application reading it finds something, and the result says SUBSTITUTED
// rather than reproduced so that nobody reads "the parameter is in the twin"
// as "the parameter says what production says".
func planSSMParameter(d provider.CloudResource) Step {
	region := awsRegion(d)
	kind := "String"
	// `value` is consumed because the parameter is created and what it holds
	// is reported as a substitution by the action itself, which is a stronger
	// statement than an unreproduced attribute and would be hidden by one.
	consumed := []string{awsRegionAttr, "value"}
	reasons := map[string]string{
		"key_id": "the AWS surface in this build does not answer for KMS, so the key " +
			"this parameter is encrypted with does not exist in the twin",
	}
	if v, ok := d.Attr("type"); ok {
		if strings.EqualFold(strings.TrimSpace(v), "SecureString") {
			// NOT consumed. The parameter is created as a plain String,
			// because a SecureString is encrypted with a KMS key and the
			// surface does not answer for KMS, so a SecureString here would be
			// a parameter the application cannot decrypt. That is a real
			// difference between the twin and production and it is reported
			// rather than smoothed over.
			reasons["type"] = "it is created as a plain String, because a SecureString is " +
				"decrypted with a KMS key and the AWS surface in this build does not answer " +
				"for KMS"
		} else {
			consumed = append(consumed, "type")
		}
	}
	return Step{
		Emulator: AWSName,
		Kind:     "parameter",
		Hosts:    []string{"ssm." + region + ".amazonaws.com"},
		Consumed: consumed,
		Reasons:  reasons,
		Actions: []Action{{
			Name: "the parameter",
			Create: []Request{{
				Method: "POST", Path: "/",
				Header: awsJSON("AmazonSSM.PutParameter", "1.1"),
				Body: fmt.Sprintf(`{"Name":%q,"Value":%q,"Type":%q,"Overwrite":true}`,
					d.Name, placeholderValue, kind),
			}},
			Verify: Request{
				Method: "POST", Path: "/",
				Header: awsJSON("AmazonSSM.GetParameter", "1.1"),
				Body:   fmt.Sprintf(`{"Name":%q}`, d.Name),
			},
			Expect: fmt.Sprintf(`"Name": %q`, d.Name),
			SubstitutedReason: "the parameter exists in the twin under its own name and holds " +
				"a placeholder, never production's value",
		}},
	}
}

// planSecret creates a secret, whose value is a placeholder for the same
// reason a parameter's is.
func planSecret(d provider.CloudResource) Step {
	region := awsRegion(d)
	return Step{
		Emulator: AWSName,
		Kind:     "secret",
		Hosts:    []string{"secretsmanager." + region + ".amazonaws.com"},
		Consumed: []string{awsRegionAttr},
		Reasons: map[string]string{
			"kms_key_id": "the AWS surface in this build does not answer for KMS, so the key " +
				"this secret is encrypted with does not exist in the twin",
			"rotation_rules": "nothing rotates a secret inside a twin, and a rotation that " +
				"did run would run against a placeholder",
			"recovery_window_in_days": "a twin is deleted whole, so a recovery window has " +
				"nothing to protect",
		},
		Actions: []Action{{
			Name: "the secret",
			Create: []Request{{
				Method: "POST", Path: "/",
				Header: awsJSON("secretsmanager.CreateSecret", "1.1"),
				// The token is required by this container and generated by the
				// SDK against the real service. Derived from the name so that
				// two runs of one declaration send the same bytes.
				Body: fmt.Sprintf(`{"Name":%q,"SecretString":%q,"ClientRequestToken":%q}`,
					d.Name, placeholderValue, requestToken(d.Name)),
			}},
			Verify: Request{
				Method: "POST", Path: "/",
				Header: awsJSON("secretsmanager.DescribeSecret", "1.1"),
				Body:   fmt.Sprintf(`{"SecretId":%q}`, d.Name),
			},
			Expect: fmt.Sprintf(`"Name": %q`, d.Name),
			SubstitutedReason: "the secret exists in the twin under its own name and holds a " +
				"placeholder, never production's value",
		}},
	}
}

// planEventBus creates an EventBridge bus.
func planEventBus(d provider.CloudResource) Step {
	region := awsRegion(d)
	return Step{
		Emulator: AWSName,
		Kind:     "event bus",
		Hosts:    []string{"events." + region + ".amazonaws.com"},
		Consumed: []string{awsRegionAttr},
		Reasons: map[string]string{
			"event_source_name": "a partner event source is created by the partner, and " +
				"there is no partner in a twin",
		},
		Actions: []Action{{
			Name: "the event bus",
			Create: []Request{{
				Method: "POST", Path: "/",
				Header: awsJSON("AWSEvents.CreateEventBus", "1.1"),
				Body:   fmt.Sprintf(`{"Name":%q}`, d.Name),
			}},
			Verify: Request{
				Method: "POST", Path: "/",
				Header: awsJSON("AWSEvents.DescribeEventBus", "1.1"),
				Body:   fmt.Sprintf(`{"Name":%q}`, d.Name),
			},
			Expect: fmt.Sprintf(`"Name": %q`, d.Name),
		}},
	}
}

// placeholderValue is what a secret and a parameter hold in a twin.
//
// It says what it is IN THE VALUE, because the reader of this string is an
// engineer staring at a failure wondering why their credential does not work,
// and the shortest path from there to understanding is the value itself saying
// so.
const placeholderValue = "antifailure-placeholder-not-productions-value"

// requestToken is the idempotency token Secrets Manager requires, derived from
// the secret's name so that one declaration always sends the same request.
//
// Shaped as a UUID because the service documents it that way and this
// container validates the length. It is not random and it is not meant to be:
// the property wanted here is that a second run of one plan is the same plan.
func requestToken(name string) string {
	sum := sha256.Sum256([]byte("antifailure-seed-" + name))
	h := hex.EncodeToString(sum[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", h[0:8], h[8:12], h[12:16], h[16:20], h[20:32])
}

// truthy reads a boolean attribute in the spellings infrastructure as code
// writes one.
func truthy(d provider.CloudResource, key string) bool {
	v, ok := d.Attr(key)
	if !ok {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "yes", "1", "enabled":
		return true
	}
	return false
}

// sortedKeys orders a map's keys, so that one declaration produces one request
// rather than one of several orderings of it.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
