// Package webhook delivers the inbound events a flow is waiting on.
//
// Most billing and messaging flows do not finish when the outbound call
// returns. They finish when the provider calls back: checkout.session
// completed, invoice.paid, the delivery receipt. In a preview environment the
// provider has nowhere to call back to, so the flow stops halfway and every
// assertion after it fails for a reason that has nothing to do with the code
// under test.
//
// So the environment sends the callback itself, signed the way the provider
// signs it, to the path the manifest names. Signing it properly is the whole
// point: an application that verifies signatures, which is every application
// that should, will reject an unsigned one, and a webhook simulator that
// cannot get past the application's own verification simulates nothing.
package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Provider is one third party's webhook shape and signing scheme.
type Provider struct {
	// Name is how a user refers to it: af webhook trigger stripe ...
	Name string
	// SecretEnv is the variable holding the signing secret, which the
	// application also reads, so that both sides agree.
	SecretEnv string
	// Events are the events this provider can send, with a sample payload.
	Events map[string]string
	// Sign returns the headers that authenticate a body.
	Sign func(body []byte, secret string, now time.Time) map[string]string
}

// Providers are the ones that ship with the engine.
var Providers = map[string]Provider{
	"stripe": {
		Name:      "stripe",
		SecretEnv: "STRIPE_WEBHOOK_SECRET",
		Sign:      signStripe,
		Events: map[string]string{
			"checkout.session.completed":    `{"object":"checkout.session","status":"complete","payment_status":"paid","mode":"subscription"}`,
			"customer.subscription.created": `{"object":"subscription","status":"active","cancel_at_period_end":false}`,
			"customer.subscription.updated": `{"object":"subscription","status":"active","cancel_at_period_end":true}`,
			"customer.subscription.deleted": `{"object":"subscription","status":"canceled"}`,
			"invoice.paid":                  `{"object":"invoice","status":"paid","paid":true,"amount_paid":0}`,
			"invoice.payment_failed":        `{"object":"invoice","status":"open","paid":false,"attempt_count":1}`,
			"payment_intent.succeeded":      `{"object":"payment_intent","status":"succeeded"}`,
		},
	},
	"github": {
		Name:      "github",
		SecretEnv: "GITHUB_WEBHOOK_SECRET",
		Sign:      signGitHub,
		// Each sample names the account it is about, because the application
		// scopes every statement it makes to that account and a payload with
		// no account is answered "the payload names no account" rather than
		// handled. The first GitHub event a new customer's control plane ever
		// receives is the installation itself, and it was missing here, so the
		// path that creates their organization was the one event nobody could
		// rehearse.
		Events: map[string]string{
			"push": `{"ref":"refs/heads/main","commits":[],` +
				`"repository":{"id":1,"full_name":"afmock/app","owner":{"login":"afmock"}},` +
				`"installation":{"id":1}}`,
			"pull_request": `{"action":"opened","number":1,` +
				`"pull_request":{"number":1,"title":"afmock","draft":false,"state":"open",` +
				`"head":{"sha":"0000000000000000000000000000000000000001","ref":"afmock",` +
				`"repo":{"full_name":"afmock/app"}},"base":{"ref":"main","repo":{"full_name":"afmock/app"}}},` +
				`"repository":{"id":1,"full_name":"afmock/app","owner":{"login":"afmock"}},` +
				`"installation":{"id":1},"sender":{"id":1,"login":"afmock"}}`,
			"installation": `{"action":"created",` +
				`"installation":{"id":1,"account":{"login":"afmock","type":"Organization"},` +
				`"repository_selection":"selected"},` +
				`"repositories":[{"id":1,"full_name":"afmock/app","private":true}],` +
				`"sender":{"id":1,"login":"afmock"}}`,
		},
	},
	"resend": {
		Name:      "resend",
		SecretEnv: "RESEND_WEBHOOK_SECRET",
		Sign:      signSvix,
		Events: map[string]string{
			"email.sent":       `{"email_id":"af_captured","to":["someone@example.test"]}`,
			"email.delivered":  `{"email_id":"af_captured","to":["someone@example.test"]}`,
			"email.bounced":    `{"email_id":"af_captured","bounce":{"type":"Permanent"}}`,
			"email.complained": `{"email_id":"af_captured"}`,
			"email.opened":     `{"email_id":"af_captured"}`,
		},
	},
}

// Names returns the providers, sorted.
func Names() []string {
	out := make([]string, 0, len(Providers))
	for n := range Providers {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// EventNames returns a provider's events, sorted.
func EventNames(provider string) []string {
	p, ok := Providers[provider]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(p.Events))
	for n := range p.Events {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// EventIDOverride is the override key that pins the provider's event
// identifier, the Stripe event id or the GitHub delivery id, instead of
// setting a payload field. Sending one event twice with the same value is how
// a retry is rehearsed.
const EventIDOverride = "event_id"

// Event is a webhook ready to deliver.
type Event struct {
	// Provider is who it appears to come from.
	Provider string
	// Type is the event name.
	Type string
	// Body is the JSON payload.
	Body []byte
	// Headers authenticate it.
	Headers map[string]string
}

// Build assembles a signed event.
//
// Overrides are merged into the sample payload's data object, so a caller can
// say which subscription the event is about without writing the whole envelope
// by hand. That is the difference between a simulator somebody uses and one
// they write a script around.
func Build(provider, eventType, secret string, overrides map[string]any, now time.Time) (Event, error) {
	p, ok := Providers[provider]
	if !ok {
		return Event{}, fmt.Errorf("webhook: no provider called %q; there is %s",
			provider, strings.Join(Names(), ", "))
	}
	sample, ok := p.Events[eventType]
	if !ok {
		return Event{}, fmt.Errorf("webhook: %s has no event called %q; it has %s",
			provider, eventType, strings.Join(EventNames(provider), ", "))
	}

	var data map[string]any
	if err := json.Unmarshal([]byte(sample), &data); err != nil {
		return Event{}, err
	}
	// The one override that is not a field of the payload. A provider retries
	// the SAME event, with the same identifier, and an application that is
	// right about that treats the second delivery as done. The identifier is
	// otherwise derived from the second the event was built in, so two
	// triggers of one event a second apart were two different events, and the
	// retry, which is the ordering a billing handler most has to get right,
	// could not be rehearsed on purpose.
	var pinned string
	for k, v := range overrides {
		if k == EventIDOverride {
			pinned, _ = v.(string)
			continue
		}
		data[k] = v
	}

	body, err := envelope(p, eventType, data, now, pinned)
	if err != nil {
		return Event{}, err
	}
	headers := map[string]string{"Content-Type": "application/json"}
	if p.Sign != nil {
		for k, v := range p.Sign(body, secret, now) {
			headers[k] = v
		}
	}
	if p.Name == "github" {
		// GitHub carries the event name in a header rather than in the body,
		// and the sender did not set it. Every delivery arrived as "unknown",
		// which the application acknowledged and acted on nowhere, so an
		// installation created and a pull request opened were both delivered,
		// both answered 200, and neither did a thing.
		headers["X-GitHub-Event"] = eventType
		// One identifier per delivery. It was a constant, so the second event
		// of any kind was fenced as a replay of the first by an application
		// that keys its ledger on it, which is exactly what a correct
		// application does. Derived like the Stripe event id, so two runs of
		// one workflow still agree.
		headers["X-GitHub-Delivery"] = deliveryID(eventType, data, now)
		if pinned != "" {
			headers["X-GitHub-Delivery"] = pinned
		}
	}
	return Event{Provider: provider, Type: eventType, Body: body, Headers: headers}, nil
}

// deliveryID is a UUID shaped identifier that is unique per delivery and the
// same for two runs of one workflow, for the same reason eventID is.
func deliveryID(eventType string, data map[string]any, now time.Time) string {
	digest := sha256.New()
	digest.Write([]byte(eventType))
	digest.Write([]byte{0})
	if payload, err := json.Marshal(data); err == nil {
		digest.Write(payload)
	}
	digest.Write([]byte(strconv.FormatInt(now.Unix(), 10)))
	h := hex.EncodeToString(digest.Sum(nil))
	return h[0:8] + "-" + h[8:12] + "-4" + h[12:15] + "-8" + h[15:18] + "-" + h[18:30]
}

// envelope wraps the data in the shape the provider actually sends.
//
// Providers do not agree on this, and an application parsing the wrong
// envelope fails on a field that is not there, which looks like a bug in the
// application rather than in the simulator.
func envelope(p Provider, eventType string, data map[string]any, now time.Time, pinned string) ([]byte, error) {
	switch p.Name {
	case "stripe":
		id := eventID(eventType, data, now)
		if pinned != "" {
			id = pinned
		}
		return json.Marshal(map[string]any{
			"id":               id,
			"object":           "event",
			"api_version":      "2024-06-20",
			"created":          now.Unix(),
			"livemode":         false,
			"type":             eventType,
			"pending_webhooks": 0,
			"request":          map[string]any{"id": nil, "idempotency_key": nil},
			"data":             map[string]any{"object": data},
		})
	case "resend":
		return json.Marshal(map[string]any{
			"type":       eventType,
			"created_at": now.UTC().Format(time.RFC3339),
			"data":       data,
		})
	default:
		// GitHub and anything modelled on it send the payload at the top
		// level, with the event name in a header rather than in the body.
		return json.Marshal(data)
	}
}

// eventID is unique per event and the same for two runs of one workflow.
//
// It used to be "evt_afmock" plus the unix second, which meant every event
// triggered in the same second carried the SAME id. That is invisible until
// somebody builds the integration this simulator exists for: a Stripe webhook
// handler must be idempotent on the event id, because Stripe retries, so a
// correct handler treats the second event of a second as a repeat of the first
// and does nothing with it. A subscription created and an invoice paid in one
// second would deliver one of them.
//
// The digest covers the event type and its payload rather than a counter or
// random bytes, so the property that earned the fixed clock is kept: two runs
// of one workflow produce the same identifiers and can be compared. Two
// triggers of the same event with the same payload in the same second still
// collide, and should: that is the same event, and an application dropping the
// repeat is behaving correctly.
func eventID(eventType string, data map[string]any, now time.Time) string {
	digest := sha256.New()
	digest.Write([]byte(eventType))
	digest.Write([]byte{0})
	// Marshalling a map sorts its keys, so the digest does not depend on
	// whatever order the overrides happened to be merged in.
	if payload, err := json.Marshal(data); err == nil {
		digest.Write(payload)
	}
	return "evt_afmock" + strconv.FormatInt(now.Unix(), 10) +
		hex.EncodeToString(digest.Sum(nil))[:8]
}

// signStripe produces the Stripe-Signature header.
//
// The scheme is HMAC-SHA256 over "timestamp.body", which is what makes a
// captured signature useless a few minutes later. An implementation that
// signed the body alone would verify today and let somebody replay it forever.
func signStripe(body []byte, secret string, now time.Time) map[string]string {
	ts := strconv.FormatInt(now.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(body)
	return map[string]string{
		"Stripe-Signature": "t=" + ts + ",v1=" + hex.EncodeToString(mac.Sum(nil)),
	}
}

// signGitHub produces the X-Hub-Signature-256 header.
func signGitHub(body []byte, secret string, _ time.Time) map[string]string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return map[string]string{
		"X-Hub-Signature-256": "sha256=" + hex.EncodeToString(mac.Sum(nil)),
	}
}

// signSvix produces the headers Svix uses, which Resend and several others
// send.
func signSvix(body []byte, secret string, now time.Time) map[string]string {
	id := "msg_afmock"
	ts := strconv.FormatInt(now.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(id + "." + ts + "."))
	mac.Write(body)
	return map[string]string{
		"svix-id":        id,
		"svix-timestamp": ts,
		"svix-signature": "v1," + hex.EncodeToString(mac.Sum(nil)),
	}
}

// Verify checks a signature the way an application would.
//
// It exists so the engine can prove its own signing is right, rather than
// asserting that some bytes were produced. A signer nobody verified is a
// signer that works until somebody's application rejects it.
func Verify(provider string, body []byte, headers map[string]string, secret string, now time.Time) bool {
	p, ok := Providers[provider]
	if !ok || p.Sign == nil {
		return false
	}
	want := p.Sign(body, secret, now)
	for k, v := range want {
		if k == "Content-Type" || strings.Contains(k, "id") || strings.Contains(k, "Delivery") {
			continue
		}
		if !hmac.Equal([]byte(headers[k]), []byte(v)) {
			return false
		}
	}
	return true
}

// SecretFor returns the signing secret an environment uses for a provider.
//
// Derived from the environment identifier rather than generated, so that two
// processes agree without sharing state: af up puts it in the services'
// environment, af webhook trigger recomputes it in a different shell an hour
// later, and both arrive at the same value. Storing it would mean a file to
// keep, a file to clean up, and a file that can be out of date.
//
// It is not a security boundary and does not need to be. Its whole job is to
// let an application's own signature verification succeed against an event
// this environment produced; nothing outside the environment can deliver one,
// because nothing outside can reach the environment.
func SecretFor(envID, provider string) string {
	mac := hmac.New(sha256.New, []byte("antifailure/webhook-secret/v1"))
	mac.Write([]byte(envID))
	mac.Write([]byte{0})
	mac.Write([]byte(provider))
	return "whsec_" + hex.EncodeToString(mac.Sum(nil))[:32]
}

// Secrets returns the signing secrets an environment uses, keyed by the
// variable each provider's secret is delivered under.
//
// One rule with a webhook_path per provider is enough. A value already set in
// the shell wins, because somebody who set one has a reason and is probably
// matching a fixture recorded elsewhere; otherwise the secret is derived from
// the environment identifier, so a preview environment needs no configuration
// at all to have working signature verification.
//
// This is the one derivation, and af up, af webhook trigger, the MCP sender
// and af explain all call it. Two copies of the rule that decides the secret
// would be two copies that can disagree, and the symptom would be an
// application refusing every event as unsigned.
func Secrets(rules []schema.EgressRule, envID string, getenv func(string) string) map[string]string {
	out := map[string]string{}
	for _, r := range rules {
		if r.WebhookPath == "" {
			continue
		}
		provider := ForHost(r.Host)
		if provider == "" {
			continue
		}
		name := SecretEnvFor(provider)
		if _, done := out[name]; done {
			continue
		}
		if getenv != nil {
			if value := getenv(name); value != "" {
				out[name] = value
				continue
			}
		}
		out[name] = SecretFor(envID, provider)
	}
	return out
}

// SecretsSourceName is how the secrets chain refers to the values Secrets
// returns, in af explain and in the "Looked in" list of AF-SEC-001.
const SecretsSourceName = "the environment's webhook signing secrets"

// SecretEnvFor returns the variable a provider's secret is read from.
func SecretEnvFor(provider string) string {
	p, ok := Providers[provider]
	if !ok {
		return ""
	}
	return p.SecretEnv
}

// ForHost returns the provider a host belongs to, if any.
//
// Used to decide which secrets an environment needs: a manifest that names a
// webhook path for Stripe needs Stripe's secret in its services, and nothing
// else needs to be configured.
func ForHost(host string) string {
	host = strings.ToLower(host)
	for name := range Providers {
		if strings.Contains(host, name) {
			return name
		}
	}
	return ""
}
