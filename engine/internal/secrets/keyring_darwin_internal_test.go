//go:build darwin

package secrets

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Every shape of password line security printed while this was written, taken
// from real runs of `security find-generic-password -g` against values stored
// with -X, and the shapes it must refuse rather than guess at.
//
// The real round trips in keyring_darwin_test.go reach the first group through
// the command. The second group cannot be produced by the command at all, which
// is why it is here: a listing this code does not recognise has to be an error,
// because the alternative is returning some part of the line as the secret.
func TestAPasswordListingIsReadInEveryShapeSecurityPrints(t *testing.T) {
	for name, tc := range map[string]struct{ listing, want string }{
		"printable, quoted":                  {`password: "plain"`, "plain"},
		"spaces at both ends":                {`password: " lead and trail "`, " lead and trail "},
		"a quote inside, left unescaped":     {`password: "a""`, `a"`},
		"text that looks like the hex form":  {`password: "0x41  "A""`, `0x41  "A"`},
		"a backslash, hex with no rendering": {`password: 0x5C `, `\`},
		"a tab, hex then its rendering":      {`password: 0x7461620978  "tab\011x"`, "tab\tx"},
		"multibyte, hex then its rendering":  {`password: 0x756E69636F64C3A9E29C93  "unicod\303\251\342\234\223"`, "unicodé✓"},
		"empty":                              {"password: ", ""},
		"among other lines":                  {"keychain: \"x\"\npassword: \"plain\"\n", "plain"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := passwordFromListing(tc.listing)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}

	for name, listing := range map[string]string{
		"no password line":         "keychain: \"x\"\n",
		"hex that does not decode": "password: 0xZZ ",
		"unquoted text":            "password: plain",
		"an opening quote only":    `password: "plain`,
	} {
		t.Run("refuses "+name, func(t *testing.T) {
			_, err := passwordFromListing(listing)
			require.Error(t, err)
		})
	}
}
