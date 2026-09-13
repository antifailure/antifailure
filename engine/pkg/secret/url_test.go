package secret

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Every address here fails to parse, and every one of them makes the standard
// library print part of its credential. The first assertion in each case
// proves that second half, so a case whose fixture stopped leaking under
// url.Parse would fail rather than pass having exercised nothing.
func TestParseURLNeverQuotesTheCredentialOfAnAddressThatDoesNotParse(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		// leaked is a piece of the credential url.Parse's own error prints.
		leaked string
		// secrets must not appear anywhere in the error ParseURL returns.
		secrets []string
		// said is what the error still has to tell the reader.
		said []string
	}{
		{
			name:    "a port that is not a number, after a password",
			raw:     "postgres://admin:Hunter2Pass@db.example.test:54x2/app",
			leaked:  "Hunter2Pass",
			secrets: []string{"Hunter2Pass", "admin:"},
			said:    []string{"invalid port", "db.example.test"},
		},
		{
			name:    "an unescaped slash in the password",
			raw:     "https://svc:Qx7Kp/Zr9Wm@vault.example.test/v1",
			leaked:  "Qx7Kp",
			secrets: []string{"Qx7Kp", "Zr9Wm", "svc:"},
			said:    []string{"not quoted", "vault.example.test"},
		},
		{
			name:    "a bad percent escape in the password",
			raw:     "https://svc:Mv4%zzTq8@vault.example.test/v1",
			leaked:  "%zz",
			secrets: []string{"Mv4", "zzTq8", "%zz"},
			said:    []string{"not quoted", "vault.example.test"},
		},
		{
			name:    "an unescaped hash in the password",
			raw:     "https://svc:Hb3#Jk7@vault.example.test",
			leaked:  "Hb3",
			secrets: []string{"Hb3", "Jk7"},
			said:    []string{"not quoted"},
		},
		{
			name:    "the host forgotten, so the password reads as a port",
			raw:     "postgres://admin:Pl8Wd/q3Z",
			leaked:  "Pl8Wd",
			secrets: []string{"Pl8Wd", "q3Z"},
			said:    []string{"invalid port"},
		},
		{
			name:    "a token in the query of an address broken in its path",
			raw:     "https://hooks.example.test/a%zz?token=Tk5Rw9",
			leaked:  "Tk5Rw9",
			secrets: []string{"Tk5Rw9"},
			said:    []string{"invalid URL escape", "hooks.example.test"},
		},
		{
			name:    "a space pasted before the scheme",
			raw:     " https://svc:Lp2Vn8@hooks.example.test/x",
			leaked:  "Lp2Vn8",
			secrets: []string{"Lp2Vn8"},
			said:    []string{"not quoted", "hooks.example.test"},
		},
		{
			name:    "an IP literal with a bad port, after a password",
			raw:     "http://u:Wq9Zt4@[::1]:8a/x",
			leaked:  "Wq9Zt4",
			secrets: []string{"Wq9Zt4"},
			said:    []string{"invalid port", "[::1]"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, stdlib := url.Parse(c.raw)
			require.Error(t, stdlib, "the fixture parses, so it tests nothing")
			require.Contains(t, stdlib.Error(), c.leaked,
				"url.Parse no longer prints this credential, so the fixture no longer shows the leak")

			u, err := ParseURL(c.raw)
			require.Nil(t, u)
			require.Error(t, err)
			for _, s := range c.secrets {
				require.NotContains(t, err.Error(), s)
			}
			for _, s := range c.said {
				require.Contains(t, err.Error(), s, "the error no longer says what is wrong")
			}
			var urlErr *url.Error
			require.True(t, errors.As(err, &urlErr),
				"a caller that asks for a *url.Error has to still get one")
		})
	}
}

func TestParseURLReturnsWhatURLParseReturnsForAnAddressThatParses(t *testing.T) {
	const raw = "postgres://admin:Hunter2Pass@db.example.test:5432/app?sslmode=require"
	want, err := url.Parse(raw)
	require.NoError(t, err)

	got, err := ParseURL(raw)
	require.NoError(t, err)
	require.Equal(t, want, got)
	password, _ := got.User.Password()
	require.Equal(t, "Hunter2Pass", password, "the address used for the connection must keep its credential")
}

func TestRedactURLKeepsWhereAnAddressPointsAndNothingThatCanHoldACredential(t *testing.T) {
	for _, c := range []struct{ name, raw, want string }{
		{"user information and a query", "postgres://admin:pw@db.example.test:5432/app?sslmode=require&password=x",
			"postgres://***@db.example.test:5432/app?***"},
		{"a webhook token in the query", "https://hooks.example.test/services/T1/B2?token=abc",
			"https://hooks.example.test/services/T1/B2?***"},
		{"a shared access signature", "https://acct.blob.core.windows.net/goldens?sv=1&sig=abc",
			"https://acct.blob.core.windows.net/goldens?***"},
		{"a token in the fragment", "https://host.example.test/p#access_token=abc", "https://host.example.test/p#***"},
		{"an @ inside the password", "https://svc:p@ss@host.example.test/x", "https://***@host.example.test/x"},
		{"a scheme relative address", "//svc:pw@host.example.test/p", "//***@host.example.test/p"},
		{"a double slash inside a password is not a scheme", "user:pa//ss@host.example.test", "***@host.example.test"},
		{"the host forgotten", "postgres://admin:hunter2", "postgres://admin:***"},
		{"the host forgotten and the password holding a slash", "postgres://admin:hun/ter2?x#y", "postgres://admin:***"},
		{"an IP literal with a bad port", "http://[::1]:x9/p", "http://[::1]:***"},
		{"nothing to remove", "https://db.example.test:5432/app", "https://db.example.test:5432/app"},
		{"an IP literal with a real port", "http://[::1]:8080/x", "http://[::1]:8080/x"},
		{"not an address at all", "not a url at all", "not a url at all"},
		{"empty", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := RedactURL(c.raw)
			require.Equal(t, c.want, got)
			require.False(t, strings.Contains(got, "[redacted]"),
				"a square bracket is not legal in user information, so the redacted address would not parse")
		})
	}
}
