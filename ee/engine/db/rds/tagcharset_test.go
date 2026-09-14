// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package rds_test

// A golden publishes on a control plane that refuses AWS's disallowed tag
// characters, because the provider encodes what it stores.
//
// A live run against real RDS had the golden's CreateDBSnapshot refused with
// InvalidParameterValue: the attestation in its tags was a document, and a tag
// value allows letters, digits, whitespace and _ . : / = + - @ only. The fake
// now refuses the same values in the same words, and these tests publish the
// kind of document that was refused.

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/cloudauth"
	"github.com/antifailure/antifailure/ee/engine/db/managed/tagvalue"
	"github.com/antifailure/antifailure/engine/conformance"
	"github.com/antifailure/antifailure/engine/pkg/airgap"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// A JSON attestation and a provenance full of punctuation publish, list as
// what was written, and branch, and the rules hash round trips through its
// encoding.
func TestAGoldenWithPunctuationInItsTagsPublishesAndReadsBack(t *testing.T) {
	server := newFake(t, conformance.DefaultSeedSQL, "")
	p := newProvider(t, server)
	ctx := context.Background()

	var masked, verified int
	attestation := `{"scanner": "conformance", "findings": 0, "tables": ["customers", "orders"]}`
	g := spec(&masked, &verified, attestation)
	// A hex digest, because that is what the engine's rules hash is, and the
	// version identifier embeds its first eight characters in a tag the
	// provider does not encode. It is still stored encoded and read back
	// decoded, which the equality below checks. The provenance is free text
	// from the manifest, so it carries the punctuation.
	g.RulesHash = "5d41402abc4b2a76b9719d911017c592"
	g.Provenance = "acme/production, eu-west-1 (golden) {v2}"

	gv, err := p.RefreshGolden(ctx, g)
	require.NoError(t, err, "a document attestation must publish on a control plane that refuses AWS's disallowed tag characters")

	goldens, err := p.ListGoldens(ctx)
	require.NoError(t, err)
	require.Len(t, goldens, 1)
	require.True(t, goldens[0].Verified)
	require.Equal(t, attestation, goldens[0].Attestation)
	require.Equal(t, g.RulesHash, goldens[0].RulesHash)
	require.Equal(t, g.Provenance, goldens[0].Provenance)

	b, err := p.Branch(ctx, gv.ID, "env_punctuated_golden")
	require.NoError(t, err)
	connection, err := p.ConnString(ctx, b, provider.ConnDirect)
	require.NoError(t, err)
	require.NoError(t, reachable(connection.Reveal()))
}

// The fake refuses a disallowed tag value in AWS's words, and accepts the same
// request with an allowed one, so the refusal is about the value.
func TestTheFakeRefusesATagValueAWSRefuses(t *testing.T) {
	server := newFake(t, conformance.DefaultSeedSQL, "")

	post := func(value string) (int, string) {
		form := url.Values{
			"Action":           {"AddTagsToResource"},
			"Version":          {"2014-10-31"},
			"ResourceName":     {"arn:aws:rds:" + testRegion + ":000000000000:db:no-such-instance"},
			"Tags.Tag.1.Key":   {"antifailure:attestation.1"},
			"Tags.Tag.1.Value": {value},
		}
		body := []byte(form.Encode())
		signed, err := cloudauth.SignV4(cloudauth.SigV4Request{
			Method: "POST", URL: server.URL(), Body: body,
			Headers: map[string]string{"Content-Type": "application/x-www-form-urlencoded; charset=utf-8"},
			Region:  testRegion, Service: "rds", Credentials: testCredentials, Now: time.Now().UTC(),
		})
		require.NoError(t, err)
		req, err := http.NewRequest("POST", server.URL(), bytes.NewReader(body))
		require.NoError(t, err)
		for k, v := range signed {
			req.Header.Set(k, v)
		}
		resp, err := airgap.Client(airgap.SiteRDS, 10*time.Second).Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		read, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode, string(read)
	}

	status, body := post(`{"findings": 0}`)
	require.Equal(t, http.StatusBadRequest, status, body)
	require.Contains(t, body, tagvalue.Refusal)

	status, body = post("eyJmaW5kaW5ncyI6IDB9")
	require.NotContains(t, body, tagvalue.Refusal,
		"an allowed value was refused too, so the refusal is not about the value (status %d)", status)
}
