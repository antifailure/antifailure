// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package cloudsql_test

// Shared helpers for the suites in this package.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/antifailure/antifailure/ee/engine/db/cloudsql"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/secret"
)

// cloneRequestForTest is the request the provider builds.
func cloneRequestForTest(destination string) any {
	return cloudsql.CloneRequestForTest(destination)
}

// goldenSpec is a masking and verification pair that succeeds.
//
// Mask and Verify are real functions rather than nil because RefreshGolden
// refuses a nil one: publishing an unmasked golden is the single thing this
// provider must never do, and a spec that could skip masking by leaving a field
// unset would make that refusal unreachable.
func goldenSpec() provider.GoldenSpec {
	return provider.GoldenSpec{
		SourceURL:  secret.New("postgres://unused"),
		Version:    16,
		RulesHash:  "rules-under-test",
		Provenance: "the cloudsql suite",
		Mask:       func(context.Context, secret.Value) error { return nil },
		Verify: func(context.Context, secret.Value) (string, error) {
			return "attested-by-the-test", nil
		},
	}
}

// postSlowClone issues a clone request that names a zone, which Google
// documents as forcing the standard workflow.
//
// It builds the JSON by hand rather than through the provider, because the
// whole point is to send a shape the provider structurally cannot produce. It
// is the positive control for the counter.
func postSlowClone(ctx context.Context, endpoint, project, source, destination string) error {
	body, err := json.Marshal(map[string]any{"cloneContext": map[string]any{
		"kind":                    "sql#cloneContext",
		"destinationInstanceName": destination,
		"preferredZone":           "us-central1-a",
	}})
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/v1/projects/%s/instances/%s/clone", endpoint, project, source)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer AF_FAKE_CLOUDSQL_TOKEN")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("slow clone returned HTTP %d", resp.StatusCode)
	}
	return nil
}
