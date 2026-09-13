package manifest_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A credential reaches a host field in one of two shapes, and both were
// printed back.
//
// A whole URL, https://deploy:<token>@registry.example.com, is refused by
// validHostPattern, and the refusal quoted it. Those are the first two tests.
//
// Bare user information, deploy:<token>@registry.example.com, was NOT refused
// at all. validHostPattern strips a port with net.SplitHostPort, which splits at
// the last colon and never checks that what follows is a number, so the value
// validated as the host "deploy" with the "port" "<token>@registry.example.com",
// and every command that lists the manifest's hosts printed it. Those are the
// last two tests.
//
// Every fixture password is lowercase. The manifest is normalized before it is
// validated, and normalization lowercases every host, so the refusal prints the
// password lowercased. A mixed case fixture made NotContains pass while the
// lowercased password was on the screen, which is how the first version of
// these tests passed against the unfixed validator.
//
// Each asserts the refusal fires before it asserts anything about the text, so a
// fixture that stops being refused fails on that line rather than passing
// NotContains against no message at all.

func TestABuildAllowHostCarryingACredentialIsRefusedWithoutIt(t *testing.T) {
	t.Parallel()
	_, err := parse(t, minimal+`    build:
      allow_hosts: ['https://deploy:mh5pass@registry.example.com']
`)
	text := messages(problems(t, err))
	require.Contains(t, text, "is not a valid hostname", "the refusal this test is about did not fire")
	require.NotContains(t, text, "mh5pass")
	require.NotContains(t, err.Error(), "mh5pass")
	require.Contains(t, text, "registry.example.com", "the refusal no longer names the host to fix")
}

func TestAnEgressRuleHostCarryingACredentialIsRefusedWithoutIt(t *testing.T) {
	t.Parallel()
	_, err := parse(t, minimal+`egress:
  rules:
    - host: 'https://deploy:mh6pass@api.example.com'
      mode: block
`)
	text := messages(problems(t, err))
	require.Contains(t, text, "is not a valid hostname", "the refusal this test is about did not fire")
	require.NotContains(t, text, "mh6pass")
	require.NotContains(t, err.Error(), "mh6pass")
	require.Contains(t, text, "api.example.com", "the refusal no longer names the host to fix")
}

func TestABuildAllowHostWithBareUserInformationIsRefusedWithoutIt(t *testing.T) {
	t.Parallel()
	_, err := parse(t, minimal+`    build:
      allow_hosts: ['deploy:mh7pass@registry.example.com']
`)
	text := messages(problems(t, err))
	require.Contains(t, text, "is not a valid hostname",
		"a host with a user and password in front of it validated as the host before the @")
	require.NotContains(t, text, "mh7pass")
	require.NotContains(t, err.Error(), "mh7pass")
}

func TestAnEgressRuleHostWithBareUserInformationIsRefusedWithoutIt(t *testing.T) {
	t.Parallel()
	_, err := parse(t, minimal+`egress:
  rules:
    - host: 'deploy:mh8pass@api.example.com'
      mode: block
`)
	text := messages(problems(t, err))
	require.Contains(t, text, "is not a valid hostname",
		"a host with a user and password in front of it validated as the host before the @")
	require.NotContains(t, text, "mh8pass")
	require.NotContains(t, err.Error(), "mh8pass")
}
