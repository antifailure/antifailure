package emulator

import (
	"fmt"
	"strings"

	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// What a Google declaration becomes inside the Google emulators.
//
// MEASURED against the digests this build pins, on 2026-09-20, and the
// measurement changed what this file claims rather than confirming it.
// fake-gcs-server ACCEPTS a location and does not honour it: a bucket created
// asking for EUROPE-WEST1 came back as US-CENTRAL1, with a 200 and no warning.
// A build that read the 200 as success would have reported a European bucket
// in the twin, and the first thing anybody tested against it would be a
// latency or a residency assumption that the twin cannot hold. So `location`
// is reported unreproduced, with what the emulator actually does in the
// reason, and the reason exists because somebody ran it rather than because
// somebody expected it.
//
// Neither Google emulator here wants credentials. The storage emulator routes
// on the Host header and answers for storage.googleapis.com itself, and the
// Pub/Sub emulator accepts any project in the path whatever it was started
// with, so both of these are ordinary requests to the provider's own hostname
// with nothing bolted onto them.

func init() {
	registerSeed("google_storage_bucket", planGCSBucket)
	registerSeed("google_pubsub_topic", planPubSubTopic)
	registerSeed("google_pubsub_subscription", planPubSubSubscription)
}

// gcpDefaultProject is the project a declaration with none is created under.
//
// It is the project the Pub/Sub emulator in this build is STARTED with, in
// gcp.go's command, so a declaration that names no project lands where an
// application configured by the same default would look for it.
const gcpDefaultProject = "af-environment"

// gcpProject reads the declaration's project.
func gcpProject(d provider.CloudResource) string {
	if p, ok := d.Attr("project"); ok && strings.TrimSpace(p) != "" {
		return strings.TrimSpace(p)
	}
	return gcpDefaultProject
}

// planGCSBucket creates a bucket through the JSON API.
func planGCSBucket(d provider.CloudResource) Step {
	project := gcpProject(d)
	body := fmt.Sprintf(`{"name":%q`, d.Name)
	consumed := []string{"project"}
	step := Step{
		Emulator: GCSName,
		Kind:     "bucket",
		Hosts:    []string{"storage.googleapis.com"},
		Reasons: map[string]string{
			"location": "the storage emulator answered US-CENTRAL1 for a bucket that asked " +
				"for another location, measured against the digest this build pins, so the " +
				"twin's bucket is not in the location production's is",
			"storage_class": "the storage emulator answers STANDARD whatever class is asked " +
				"for, so a class reproduced here would be a class that is not held",
			"lifecycle_rule": "nothing in the storage emulator expires an object, so a rule " +
				"reproduced here would be a rule that does nothing",
			"uniform_bucket_level_access": "the storage emulator does not enforce access " +
				"control at all, so there is no difference in the twin between a bucket that " +
				"has this and one that does not",
			"encryption": "the twin has no key management service, so the key this bucket is " +
				"encrypted with does not exist in it",
		},
	}
	if versioningEnabled(d) {
		consumed = append(consumed, "versioning")
		body += `,"versioning":{"enabled":true}`
	}
	body += "}"
	step.Consumed = consumed
	step.Actions = []Action{{
		Name: "the bucket",
		Create: []Request{{
			Method: "POST",
			Path:   "/storage/v1/b",
			Query:  "project=" + project,
			Header: map[string]string{"Content-Type": "application/json"},
			Body:   body,
		}},
		Verify: Request{Method: "GET", Path: "/storage/v1/b/" + d.Name},
		Expect: fmt.Sprintf(`"name":%q`, d.Name),
	}}
	if versioningEnabled(d) {
		step.Actions = append(step.Actions, Action{
			Name:   "versioning",
			Verify: Request{Method: "GET", Path: "/storage/v1/b/" + d.Name},
			Expect: `"versioning":{"enabled":true}`,
		})
	}
	return step
}

// planPubSubTopic creates a topic.
func planPubSubTopic(d provider.CloudResource) Step {
	project := gcpProject(d)
	path := fmt.Sprintf("/v1/projects/%s/topics/%s", project, d.Name)
	return Step{
		Emulator: PubSubName,
		Kind:     "topic",
		Hosts:    []string{"pubsub.googleapis.com"},
		Consumed: []string{"project"},
		Reasons: map[string]string{
			"message_retention_duration": "a twin is not up long enough for a retention " +
				"period to decide anything, and the emulator holds messages in memory until " +
				"the environment goes",
			"kms_key_name": "the twin has no key management service, so the key this topic " +
				"is encrypted with does not exist in it",
			"message_storage_policy": "the emulator has no notion of a region, so a policy " +
				"naming the regions a message may be stored in has nothing to constrain",
		},
		Actions: []Action{{
			Name: "the topic",
			Create: []Request{{
				Method: "PUT",
				Path:   path,
				Header: map[string]string{"Content-Type": "application/json"},
				Body:   "{}",
			}},
			Verify: Request{Method: "GET", Path: path},
			Expect: fmt.Sprintf("projects/%s/topics/%s", project, d.Name),
		}},
	}
}

// planPubSubSubscription creates a subscription against its topic.
//
// A subscription whose declaration names no topic produces no request. Pub/Sub
// refuses one, and sending it anyway would put the emulator's refusal in front
// of somebody as a broken twin when what is missing is in the declaration.
func planPubSubSubscription(d provider.CloudResource) Step {
	project := gcpProject(d)
	path := fmt.Sprintf("/v1/projects/%s/subscriptions/%s", project, d.Name)
	step := Step{
		Emulator: PubSubName,
		Kind:     "subscription",
		Hosts:    []string{"pubsub.googleapis.com"},
		Consumed: []string{"project", "topic"},
		Reasons: map[string]string{
			"push_config": "a push subscription delivers to a URL, and an emulator with no " +
				"route out cannot deliver to one",
			"dead_letter_policy": "the queue it names is a separate declaration, and wiring " +
				"one subscription to another is not something this build does yet",
			"retry_policy": "the emulator redelivers on its own schedule and does not read a " +
				"retry policy",
		},
	}
	topic, ok := d.Attr("topic")
	if !ok || strings.TrimSpace(topic) == "" {
		step.Outcomes = append(step.Outcomes, Outcome{
			Name:  d.Name,
			State: Unmeasured,
			Reason: "the declaration names no topic, and Pub/Sub refuses a subscription " +
				"without one, so nothing was sent and the twin has no subscription of this name",
		})
		return step
	}
	topic = qualifiedTopic(project, strings.TrimSpace(topic))
	body := fmt.Sprintf(`{"topic":%q`, topic)
	if v, ok := d.Attr("ack_deadline_seconds"); ok && strings.TrimSpace(v) != "" {
		step.Consumed = append(step.Consumed, "ack_deadline_seconds")
		body += fmt.Sprintf(`,"ackDeadlineSeconds":%s`, strings.TrimSpace(v))
	}
	body += "}"
	step.Actions = []Action{{
		Name: "the subscription",
		Create: []Request{{
			Method: "PUT",
			Path:   path,
			Header: map[string]string{"Content-Type": "application/json"},
			Body:   body,
		}},
		Verify: Request{Method: "GET", Path: path},
		Expect: fmt.Sprintf(`"topic": %q`, topic),
	}}
	if v, ok := d.Attr("ack_deadline_seconds"); ok && strings.TrimSpace(v) != "" {
		step.Actions = append(step.Actions, Action{
			Name:   "ack_deadline_seconds",
			Verify: Request{Method: "GET", Path: path},
			Expect: fmt.Sprintf(`"ackDeadlineSeconds": %s`, strings.TrimSpace(v)),
		})
	}
	return step
}

// qualifiedTopic is the topic in the spelling the API uses.
//
// A declaration may carry either a bare name or the full projects/p/topics/t
// path, because infrastructure as code carries whichever the author wrote and
// a reader passes it through. Sending a bare name would create a subscription
// on a topic called "projects" in a project called nothing.
func qualifiedTopic(project, topic string) string {
	if strings.HasPrefix(topic, "projects/") {
		return topic
	}
	return fmt.Sprintf("projects/%s/topics/%s", project, topic)
}
