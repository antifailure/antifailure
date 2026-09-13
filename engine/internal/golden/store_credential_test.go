package golden_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/golden"
)

// Each store redacted the address it printed and then wrapped url.Parse's own
// error beside it, and that error quotes the address whole. A storage URL with
// one wrong character printed its shared access signature or its password
// twice, once hidden and once not.
func TestAStorageURLThatDoesNotParseIsRefusedWithoutItsCredential(t *testing.T) {
	for _, c := range []struct {
		name   string
		kind   golden.Kind
		url    string
		secret string
	}{
		{"azure_blob", golden.KindAzureBlob,
			"https://acct.blob.core.windows.net:44x3/goldens?sv=2024-01-01&sig=Az5Sig9Qw", "Az5Sig9Qw"},
		{"s3", golden.KindS3, "https://AKIDEXAMPLE:S3Sec9Kw@minio.example.test:9x0/goldens", "S3Sec9Kw"},
		{"gcs", golden.KindGCS, "https://svc:Gc5Pass8@storage.example.test:9x0/goldens", "Gc5Pass8"},
		{"local", golden.KindLocal, "file://user:Lc4Pass7@host.example.test:9x0/tmp/goldens", "Lc4Pass7"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := golden.OpenStore(c.kind, c.url, nil, nil)
			require.Error(t, err)
			require.NotContains(t, err.Error(), c.secret)
			require.Contains(t, err.Error(), "invalid port", "the refusal no longer says what is wrong")
		})
	}
}
