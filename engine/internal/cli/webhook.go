package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/webhook"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Most billing and messaging flows do not finish when the outbound call
// returns. They finish when the provider calls back, and in a preview
// environment the provider has nowhere to call back to, so the flow stops
// halfway and every assertion after it fails for a reason that has nothing to
// do with the code under test.

// WebhookJSON is the machine readable result of a delivery.
type WebhookJSON struct {
	Provider string `json:"provider"`
	Event    string `json:"event"`
	Service  string `json:"service"`
	URL      string `json:"url"`
	Status   int    `json:"status"`
	Body     string `json:"body,omitempty"`
	Duration string `json:"duration"`
	Signed   bool   `json:"signed"`
}

func newWebhookCommand(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "webhook",
		Short: "Send the inbound events a flow is waiting on",
		Long: strings.TrimSpace(`
Sends a provider's callback into the environment, signed the way that provider
signs it.

The signature is the point. An application that verifies signatures, which is
every application that should, will reject an unsigned event, and a simulator
that cannot get past the application's own verification simulates nothing.`),
	}
	cmd.AddCommand(newWebhookTriggerCommand(env))
	cmd.AddCommand(newWebhookListCommand(env))
	return cmd
}

func newWebhookListCommand(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the providers and events that can be sent",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				events := webhook.EventNames(args[0])
				if events == nil {
					return aferrors.Coded(aferrors.AFNET002,
						"request", args[0],
						"detail", "no provider called that; there is "+strings.Join(webhook.Names(), ", "))
				}
				if env.Out.Format == FormatJSON {
					return env.Out.JSON(events)
				}
				env.Out.Section(args[0] + " events")
				for _, e := range events {
					env.Out.Printf("  %s\n", e)
				}
				return nil
			}

			if env.Out.Format == FormatJSON {
				all := map[string][]string{}
				for _, p := range webhook.Names() {
					all[p] = webhook.EventNames(p)
				}
				return env.Out.JSON(all)
			}
			env.Out.Section("Webhook providers")
			for _, p := range webhook.Names() {
				env.Out.Printf("  %-8s %d events, signed with %s\n",
					p, len(webhook.EventNames(p)), webhook.Providers[p].SecretEnv)
			}
			env.Out.Println("")
			env.Out.Hint("See one provider's events with", "af webhook list stripe")
			return nil
		},
	}
}

func newWebhookTriggerCommand(env *Env) *cobra.Command {
	var branch, service, path, secret string
	var overrides []string
	cmd := &cobra.Command{
		Use:   "trigger <provider> <event>",
		Short: "Send one signed event into the environment",
		Long: strings.TrimSpace(`
The path is taken from the manifest's webhook_path for that provider unless
--path says otherwise, and the signing secret from the same variable the
application reads, so both sides agree without anybody configuring twice.`),
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			provider, eventType := args[0], args[1]

			o, m, err := orchestratorWithManifest(env, branch)
			if err != nil {
				return err
			}
			if path == "" {
				path = webhookPathFor(m, provider)
			}
			if path == "" {
				return aferrors.Coded(aferrors.AFNET012,
					"service", orAny(service),
					"detail", "no webhook_path is set for "+provider+" in the manifest, and --path was not given")
			}
			if secret == "" {
				// The same value the services received, so the application's
				// own verification succeeds without anybody configuring it.
				secret = o.WebhookSecretFor(provider)
			}

			values, err := parseOverrides(overrides)
			if err != nil {
				return err
			}
			event, err := webhook.Build(provider, eventType, secret, values, env.Clock.Now())
			if err != nil {
				return aferrors.Coded(aferrors.AFNET002, "request", provider+" "+eventType,
					"detail", err.Error())
			}

			delivery, err := o.DeliverWebhook(cmd.Context(), service, path, event.Body, event.Headers)
			if err != nil {
				return err
			}

			if env.Out.Format == FormatJSON {
				return env.Out.JSON(WebhookJSON{
					Provider: provider, Event: eventType, Service: delivery.Service,
					URL: delivery.URL, Status: delivery.Status, Body: delivery.Body,
					Duration: delivery.Duration.Round(time.Millisecond).String(),
					Signed:   secret != "",
				})
			}

			symbol, style := SymbolFail, StyleBad
			if delivery.Status >= 200 && delivery.Status < 300 {
				symbol, style = SymbolOK, StyleGood
			}
			env.Out.Status(env.Out.S(style, symbol),
				fmt.Sprintf("%s %s", provider, eventType),
				fmt.Sprintf("%d from %s in %s", delivery.Status, delivery.URL,
					delivery.Duration.Round(time.Millisecond)))
			if delivery.Status >= 300 && delivery.Body != "" {
				// Where an application puts the reason it rejected an event,
				// which is almost always the signature or the shape.
				for _, line := range lastLines(delivery.Body, 8) {
					env.Out.Printf("      %s\n", env.Out.S(StyleDim, line))
				}
			}
			if delivery.Status >= 400 {
				return aferrors.Coded(aferrors.AFNET012, "service", delivery.Service,
					"detail", fmt.Sprintf("the application answered %d", delivery.Status))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&service, "service", "", "Service to deliver to, defaulting to the first reachable one")
	cmd.Flags().StringVar(&path, "path", "", "Path to deliver to, defaulting to the manifest's webhook_path")
	cmd.Flags().StringVar(&secret, "secret", "", "Signing secret, defaulting to the provider's variable in this shell")
	cmd.Flags().StringArrayVar(&overrides, "set", nil, "Set a field on the event payload, as key=value")
	cmd.Flags().StringVar(&branch, "branch", "", "Branch to deliver to, defaulting to the checked out one")
	return cmd
}

// webhookPathFor finds the path a provider's callbacks go to.
func webhookPathFor(m *schema.Manifest, provider string) string {
	if m == nil || m.Egress == nil {
		return ""
	}
	for _, r := range m.Egress.Rules {
		if r.WebhookPath != "" && strings.Contains(r.Host, provider) {
			return r.WebhookPath
		}
	}
	// A single webhook path with no provider in its host is still the one the
	// user meant, because nobody configures two by accident.
	for _, r := range m.Egress.Rules {
		if r.WebhookPath != "" {
			return r.WebhookPath
		}
	}
	return ""
}

// parseOverrides turns --set key=value into a payload patch.
//
// Values that parse as JSON are used as JSON, so --set amount_paid=4900 sets a
// number and --set paid=true sets a boolean. An application that checks a
// numeric field against a string fails in a way that looks like its own bug.
func parseOverrides(pairs []string) (map[string]any, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	out := make(map[string]any, len(pairs))
	for _, pair := range pairs {
		key, raw, found := strings.Cut(pair, "=")
		if !found || key == "" {
			return nil, aferrors.Coded(aferrors.AFNET002,
				"request", pair, "detail", "a --set value looks like key=value")
		}
		var value any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			value = raw
		}
		out[key] = value
	}
	return out, nil
}

func orAny(s string) string {
	if s == "" {
		return "any"
	}
	return s
}
