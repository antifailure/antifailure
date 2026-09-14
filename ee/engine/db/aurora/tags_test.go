// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package aurora_test

// The spelling of a tag list on the wire, and why the fake refuses the other.
//
// RDS speaks the query protocol, where a list is flattened into numbered form
// fields and the name in the middle comes from the service model. botocore's
// QuerySerializer._serialize_type_list, in botocore/serialize.py, takes that
// name from the list member's serialization name and falls back to "member"
// only when the model gives none. The RDS model, botocore/data/rds/2014-10-31/
// service-2.json, gives TagList's member the locationName "Tag". So every
// official SDK sends Tags.Tag.1.Key, and this provider sent Tags.member.1.Key
// while fakerds parsed exactly that spelling: the suite agreed with the
// provider, so it could not say no. lane-rds found it, writing the RDS provider
// against the same model.
//
// The two botocore files the rule was read from, as they stood on botocore's
// develop branch. A git blob hash is the content, so these pin it exactly:
//
//	botocore/data/rds/2014-10-31/service-2.json
//	  blob   d6264e8fa036977425ba7c8a8bdb58af7513b288
//	  sha256 27fc5b72e4bd43c4efe708c8d48fc3e8a8f539373abf35fb649986346f5699bf
//	  last changed by 0ba3a7417517f5a49f1c7f0d0072803bee1dbaec, 2026-08-27
//	botocore/serialize.py
//	  blob   547d5aa12f9a64e49942c5b0560ada5b5b2becd9
//	  sha256 fe3a276ea81fb75d35b9c579c44c4eaa5d684f629d0f7fed057ecab7f2a1f3ff
//	  last changed by 262e5e603f8949a02859fc6ac568cfcc48aa0ddb, 2026-09-11
//
// What is NOT known is whether AWS also accepts the member spelling, because
// nobody who wrote this provider has an AWS account. The spelling sent now is
// the one AWS's own clients send, so it is right whichever way that falls.

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/cloudauth"
	"github.com/antifailure/antifailure/ee/engine/db/aurora/fakerds"
)

// signedPost sends one query action to the fake, signed the way the provider
// signs, so that a refusal is about the form and not about the signature.
func signedPost(t *testing.T, server *fakerds.Server, form url.Values) (int, string) {
	t.Helper()
	body := []byte(form.Encode())
	signed, err := cloudauth.SignV4(cloudauth.SigV4Request{
		Method: "POST", URL: server.URL(), Body: body,
		Headers: map[string]string{"Content-Type": "application/x-www-form-urlencoded; charset=utf-8"},
		Region:  testRegion, Service: "rds",
		Credentials: testCredentials, Now: time.Now().UTC(),
	})
	require.NoError(t, err)
	req, err := http.NewRequest("POST", server.URL(), strings.NewReader(string(body)))
	require.NoError(t, err)
	for k, v := range signed {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	reply, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(reply)
}

func TestTagsAreSentInTheSpellingEverySDKSends(t *testing.T) {
	server := newFake(t, seedSQL, "")

	// The fake can say no. The same action the provider uses, signed correctly,
	// carrying the member spelling, is refused on the spelling.
	status, reply := signedPost(t, server, url.Values{
		"Action":              {"AddTagsToResource"},
		"Version":             {"2014-10-31"},
		"ResourceName":        {"arn:aws:rds:" + testRegion + ":123456789012:cluster:" + sourceCluster},
		"Tags.member.1.Key":   {"af-spelling-probe"},
		"Tags.member.1.Value": {"member"},
	})
	require.Equalf(t, http.StatusBadRequest, status, "the fake accepted Tags.member.N: %s", reply)
	require.Contains(t, reply, "Tags.member.N")

	// And the provider does not send it. A golden is a restore carrying tags,
	// so publishing one through the fake crosses the refusal above.
	p := newProvider(t, server)
	golden, _ := spec("t4g5t4g5")
	_, err := p.RefreshGolden(context.Background(), golden)
	require.NoError(t, err, "the provider's tags were refused by the fake, so it is sending a "+
		"spelling no AWS SDK sends")

	sent := server.LastRequest("RestoreDBClusterToPointInTime")
	require.NotEmpty(t, sent.Get("Tags.Tag.1.Key"), "the restore carried no Tags.Tag.1.Key")
	for key := range sent {
		require.NotContainsf(t, key, ".member.", "the restore carried %s", key)
	}
}
