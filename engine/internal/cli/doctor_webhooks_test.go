package cli

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The manifest the evaluator was working against on 2026-09-06: one
// captured outbound rule for api.resend.com, no webhook_path anywhere, and
// no service carrying a Stripe or GitHub secret. The application's startup
// log said billing and the GitHub App were off; nothing in the product said
// so before a delivery was refused with AF-NET-012.
func dogfoodLikeManifest() *schema.Manifest {
	return &schema.Manifest{
		Services: []schema.Service{{Name: "api", Env: []schema.EnvVar{
			{Name: "AF_RESEND_API_KEY", Value: "capture-mode-needs-no-key"},
		}}},
		Egress: &schema.Egress{Rules: []schema.EgressRule{
			{Host: "api.resend.com", Mode: schema.ModeCapture},
		}},
	}
}

func TestWebhookDelivery_SaysWhichProvidersAreOffBeforeAnythingIsSent(t *testing.T) {
	r := describeWebhookDelivery(webhookRoutingOf(dogfoodLikeManifest()))

	require.Equal(t, CheckPass, r.Status, "off is a state, not a problem")
	require.Contains(t, r.Detail, "github")
	require.Contains(t, r.Detail, "stripe")
	require.Contains(t, r.Detail, "off: no webhook_path in the manifest")
	require.Contains(t, r.Detail, "AF-NET-012", "the refusal is named before it is met")
	require.Contains(t, r.Detail, "resend (called, never calling back)",
		"an outbound capture is not an inbound configuration")
	require.Contains(t, r.Remediation, "webhook_path")
}

func TestWebhookDelivery_ARoutedProviderIsNamedWithItsPath(t *testing.T) {
	m := dogfoodLikeManifest()
	m.Egress.Rules = append(m.Egress.Rules, schema.EgressRule{
		Host: "api.stripe.com", Mode: schema.ModeMock, WebhookPath: "/webhooks/stripe",
	})

	routing := webhookRoutingOf(m)
	require.Equal(t, "/webhooks/stripe", routing["stripe"].Routed)
	require.Empty(t, routing["github"].Routed, "one provider's path is not another's")

	r := describeWebhookDelivery(routing)
	require.Equal(t, CheckPass, r.Status)
	require.True(t, strings.HasPrefix(r.Detail, "delivered to stripe at /webhooks/stripe"), r.Detail)
	require.Contains(t, r.Remediation, "af webhook send")
}

func TestWebhookDelivery_ASecretWithNoPathIsHalfAConfiguration(t *testing.T) {
	m := dogfoodLikeManifest()
	m.Services[0].Env = append(m.Services[0].Env, schema.EnvVar{Name: "GITHUB_WEBHOOK_SECRET"})

	routing := webhookRoutingOf(m)
	require.True(t, routing["github"].HasSecret)

	r := describeWebhookDelivery(routing)
	require.Equal(t, CheckWarn, r.Status, "the application will verify deliveries nothing can make")
	require.Contains(t, r.Detail, "github has its signing secret in a service but no webhook_path")
	require.Contains(t, r.Remediation, "Set webhook_path")
}

func TestWebhookDelivery_WithoutAManifestIsASkipAndNotAVerdict(t *testing.T) {
	r := checkWebhookDelivery(context.Background(), &Env{WorkDir: t.TempDir()}, nil)

	require.Equal(t, CheckSkip, r.Status)
	require.Contains(t, r.Detail, "no manifest")
}

func TestWebhookDelivery_ReadsTheManifestOnDisk(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "antifailure.yaml"), []byte(
		"version: 1\nname: fixture\nservices:\n  - name: web\n    kind: web\n    path: .\n"+
			"    port: 3000\n    build:\n      strategy: dockerfile\n      dockerfile: Dockerfile\n"+
			"egress:\n  default: block\n  rules:\n    - host: api.stripe.com\n      mode: mock\n"+
			"      webhook_path: /hooks/stripe\n"), 0o600))

	r := checkWebhookDelivery(context.Background(), &Env{WorkDir: dir}, nil)

	require.Equal(t, CheckPass, r.Status, r.Detail)
	require.Contains(t, r.Detail, "stripe at /hooks/stripe")
}

func TestDoctorCatalogIncludesWebhookDelivery(t *testing.T) {
	// A check that exists and is not in the catalog is a check nobody runs.
	// Compared by function identity rather than by running the catalog,
	// because the catalog's other checks read the real machine.
	want := reflect.ValueOf(checkWebhookDelivery).Pointer()
	for _, c := range doctorChecks {
		if reflect.ValueOf(c).Pointer() == want {
			return
		}
	}
	t.Fatal("checkWebhookDelivery is not in doctorChecks")
}
