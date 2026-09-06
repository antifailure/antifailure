package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/internal/webhook"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// webhookRouting is what the manifest says about one provider's deliveries.
type webhookRouting struct {
	// Routed is the webhook_path on an egress rule for the provider's host,
	// which is where a delivery would be sent, or empty.
	Routed string
	// HasSecret is true when a service carries the provider's signing
	// secret, which is an application expecting to verify deliveries. With
	// no path to deliver to, that is a provider somebody set up half of.
	HasSecret bool
	// Outbound is true when an egress rule names the provider's host. That
	// says the application calls the provider; it says nothing about the
	// provider calling the application, and is reported so a captured
	// outbound rule is not mistaken for a configured inbound one.
	Outbound bool
}

// checkWebhookDelivery says, before anything is sent, which providers'
// events can reach a service in the environment and which cannot.
//
// A reviewer on 2026-09-06 learned that billing and the GitHub App were off
// in the environment under rehearsal only by sending a webhook, getting
// AF-NET-012 back, and then reading the application's own startup log
// through another tool. The manifest knew all along: no egress rule set a
// webhook_path for any provider, so nothing in the environment could receive
// a Stripe or GitHub event, and no service carried either provider's secret.
// This check states that up front, next to the daemon and the disk, so it is
// read before the first delivery is refused rather than deduced after.
//
// It is informational. A provider that is not in the manifest is off in this
// environment, which is the right state for an application that does not
// use it, so the result is a pass whose detail says what is on and what is
// off. A provider set up half way, a service carrying its signing secret
// with no webhook_path to deliver to, is the one state that is almost
// certainly a mistake, and that one is a warning. An outbound rule for the
// provider's host is not half a configuration: this repository's own
// manifest captures api.resend.com to read sign in mail and receives nothing
// from Resend, and a warning on every run of af doctor here would be noise.
func checkWebhookDelivery(_ context.Context, env *Env, _ Prober) CheckResult {
	r := CheckResult{Name: "Webhook delivery"}
	path, err := manifest.Find(env.WorkDir)
	if err != nil {
		r.Status = CheckSkip
		r.Detail = "no manifest, so there is nothing to route a delivery to"
		r.Remediation = "No action needed here; the manifest check above says what to do."
		return r
	}
	m, err := manifest.Load(path)
	if err != nil {
		r.Status = CheckSkip
		r.Detail = "not checked because the manifest did not load"
		r.Remediation = "No action needed here; the manifest check above says what to do."
		return r
	}
	return describeWebhookDelivery(webhookRoutingOf(m))
}

// webhookRoutingOf reads the manifest for every provider the engine knows.
func webhookRoutingOf(m *schema.Manifest) map[string]webhookRouting {
	out := map[string]webhookRouting{}
	for _, name := range webhook.Names() {
		entry := webhookRouting{}
		if m.Egress != nil {
			for _, rule := range m.Egress.Rules {
				if webhook.ForHost(rule.Host) != name {
					continue
				}
				entry.Outbound = true
				if rule.WebhookPath != "" && entry.Routed == "" {
					entry.Routed = rule.WebhookPath
				}
			}
		}
		secret := webhook.SecretEnvFor(name)
		for _, s := range m.Services {
			for _, v := range s.Env {
				if v.Name == secret {
					entry.HasSecret = true
				}
			}
		}
		out[name] = entry
	}
	return out
}

func describeWebhookDelivery(routing map[string]webhookRouting) CheckResult {
	r := CheckResult{Name: "Webhook delivery", Status: CheckPass}
	names := make([]string, 0, len(routing))
	for name := range routing {
		names = append(names, name)
	}
	sort.Strings(names)

	var routed, half, off []string
	for _, name := range names {
		entry := routing[name]
		switch {
		case entry.Routed != "":
			routed = append(routed, name+" at "+entry.Routed)
		case entry.HasSecret:
			half = append(half, name)
		case entry.Outbound:
			off = append(off, name+" (called, never calling back)")
		default:
			off = append(off, name)
		}
	}

	var parts []string
	if len(routed) > 0 {
		parts = append(parts, "delivered to "+strings.Join(routed, ", "))
	}
	if len(half) > 0 {
		r.Status = CheckWarn
		verb := "deliveries are"
		if len(half) == 1 {
			verb = "a delivery is"
		}
		parts = append(parts, fmt.Sprintf("%s has its signing secret in a service but no "+
			"webhook_path on an egress rule, so %s refused (AF-NET-012)",
			strings.Join(half, " and "), verb))
	}
	if len(off) > 0 {
		whose, them := "their", "them"
		if len(off) == 1 {
			whose, them = "its", "it"
		}
		parts = append(parts, fmt.Sprintf("%s off: no webhook_path in the manifest, so a "+
			"delivery is refused (AF-NET-012), nothing in the environment receives %s events, "+
			"and whatever the application switches on for %s stays off",
			strings.Join(off, " and "), whose, them))
	}
	r.Detail = strings.Join(parts, "; ")

	switch {
	case len(half) > 0:
		r.Remediation = "Set webhook_path on the provider's egress rule to the route the " +
			"application receives its events on, so 'af webhook send' and send_webhook_event " +
			"have somewhere to deliver. 'af webhook list' names the events each provider can send."
	case len(routed) == 0:
		r.Remediation = "No action needed if the application does not receive webhooks. To " +
			"rehearse one, add an egress rule for the provider's host with webhook_path set " +
			"and give the service the provider's signing secret; until then every delivery " +
			"is refused with AF-NET-012."
	default:
		r.Remediation = "No action needed. 'af webhook send <provider> <event>' delivers a " +
			"signed event to the path shown."
	}
	return r
}
