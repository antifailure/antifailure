package cli

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/runtime/local"
)

// nonZero fills every settable field of v with a distinguishable value, so
// that anything still zero afterwards was not copied rather than merely
// copied as empty.
func nonZero(v reflect.Value) {
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		switch f.Kind() {
		case reflect.String:
			f.SetString("x")
		case reflect.Bool:
			f.SetBool(true)
		case reflect.Int, reflect.Int64:
			f.SetInt(7)
		case reflect.Uint, reflect.Uint64:
			f.SetUint(7)
		}
	}
}

// A key af net log publishes and never assigns is worse than a missing key: it
// parses, it validates, and it reports the zero value as though it were the
// answer. So this fills every field the sidecar decoded and requires that
// every field of the document came out non zero.
//
// It is the companion to the json tag diff in cmd/af-proxy: that one catches a
// fact with nowhere to go, this one catches a place with nothing put in it.
func TestEveryKeyAfNetLogPublishesIsActuallyFilledIn(t *testing.T) {
	var d local.Decision
	nonZero(reflect.ValueOf(&d).Elem())

	doc := reflect.ValueOf(decisionDoc(d))
	for i := 0; i < doc.NumField(); i++ {
		name, _, _ := strings.Cut(doc.Type().Field(i).Tag.Get("json"), ",")
		require.False(t, doc.Field(i).IsZero(),
			"af net log -o json declares %s and decisionDoc never assigns it, so the key is published empty whatever the sidecar decided",
			name)
	}
}

// And the question the sandbox exists to answer, asked the way a script asks
// it: is the substitution reported, and reported as false rather than absent
// when it did not happen.
func TestTheJSONSaysWhetherTheCredentialWasSwapped(t *testing.T) {
	swapped, err := json.Marshal(decisionDoc(local.Decision{
		Host: "api.stripe.com", Method: "POST", TLS: true, Path: "/v1/charges",
		Allowed: true, Status: 200, Substituted: true,
	}))
	require.NoError(t, err)
	require.Contains(t, string(swapped), `"substituted":true`)

	live, err := json.Marshal(decisionDoc(local.Decision{
		Host: "api.stripe.com", Method: "POST", TLS: true, Path: "/v1/charges",
		Allowed: true, Status: 200,
	}))
	require.NoError(t, err)
	require.Contains(t, string(live), `"substituted":false`,
		"a swap that did not happen has to be reported, not omitted; a missing key and a build that cannot report swaps look the same")
}
