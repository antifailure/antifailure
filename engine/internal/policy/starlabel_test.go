package policy

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// A star that is not the leading one stands for exactly one label.
//
// It exists because every regional AWS service is
// <service>.<region>.amazonaws.com and every virtual hosted bucket is
// <bucket>.s3.<region>.amazonaws.com. Without this shape the only pattern that
// reaches S3 is *.amazonaws.com, which also reaches SES, SQS, STS and
// Secrets Manager, and a catalog offered that choice takes the wildcard. It
// did, under a mail rule, and an S3 PUT was answered as a delivered email.

func TestEvaluate_AStarInTheMiddleCoversOneLabelAndNoMore(t *testing.T) {
	t.Parallel()
	e := engine(t, schema.ModeBlock, rule("email.*.amazonaws.com", schema.ModeCapture))

	for _, host := range []string{
		"email.us-east-1.amazonaws.com",
		"email.eu-west-2.amazonaws.com",
	} {
		d := e.Evaluate(Request{Host: host, TLS: true, Path: "/"})
		require.Equal(t, schema.ModeCapture, d.Mode, "%s is an SES endpoint", host)
	}

	for name, host := range map[string]string{
		"another service in the same region": "s3.us-east-1.amazonaws.com",
		"the star covering nothing":          "email.amazonaws.com",
		"the star covering two labels":       "email.us.east.amazonaws.com",
		"a suffix that only looks right":     "email.us-east-1.amazonaws.com.evil.test",
		"a prefix that only looks right":     "notemail.us-east-1.amazonaws.com",
	} {
		t.Run(name, func(t *testing.T) {
			d := e.Evaluate(Request{Host: host, TLS: true, Path: "/"})
			require.Equal(t, schema.ModeBlock, d.Mode, "%s must not match", host)
			require.False(t, d.Matched(), "%s must not match", host)
		})
	}
}

func TestEvaluate_AStarInTheMiddleBeatsAWildcardOverTheSameDomain(t *testing.T) {
	t.Parallel()
	// The wildcard is written first, so order must not decide. This is the
	// pair the catalog now writes: mail is captured, everything else in the
	// cloud is refused, and neither can reach the other's hosts.
	e := engine(t, schema.ModeBlock,
		rule("*.amazonaws.com", schema.ModeBlock),
		rule("email.*.amazonaws.com", schema.ModeCapture),
	)
	mail := e.Evaluate(Request{Host: "email.us-east-1.amazonaws.com", TLS: true, Path: "/"})
	require.Equal(t, schema.ModeCapture, mail.Mode)
	require.Equal(t, "email.*.amazonaws.com", mail.RuleHost)

	object := e.Evaluate(Request{Host: "s3.us-east-1.amazonaws.com", TLS: true, Path: "/b/k"})
	require.Equal(t, schema.ModeBlock, object.Mode)
	require.Equal(t, "*.amazonaws.com", object.RuleHost)
}

func TestEvaluate_ALeadingStarStillCoversOneLabelOrMore(t *testing.T) {
	t.Parallel()
	// Narrowing the leading star to exactly one label would silently change
	// every *.example.com rule already written, so it keeps the meaning it has
	// always had even when a second star appears later in the pattern.
	e := engine(t, schema.ModeBlock, rule("*.s3.*.amazonaws.com", schema.ModeBlock))
	for _, host := range []string{
		"bucket.s3.us-east-1.amazonaws.com",
		"deep.nested.bucket.s3.eu-west-1.amazonaws.com",
	} {
		d := e.Evaluate(Request{Host: host, TLS: true, Path: "/k"})
		require.True(t, d.Matched(), "%s is a virtual hosted bucket", host)
	}
	// The leading star still covers at least one label, so the apex form of
	// the same service is a different rule's business.
	d := e.Evaluate(Request{Host: "s3.us-east-1.amazonaws.com", TLS: true, Path: "/k"})
	require.False(t, d.Matched())
}

func TestEvaluate_TheReasonNamesThePatternThatDecided(t *testing.T) {
	t.Parallel()
	e := engine(t, schema.ModeBlock, rule("sqs.*.amazonaws.com", schema.ModeBlock))
	d := e.Evaluate(Request{Host: "sqs.eu-west-1.amazonaws.com", TLS: true, Path: "/"})
	require.Contains(t, d.Reason(), "sqs.*.amazonaws.com",
		"a developer reading a refusal has to be able to find the rule that produced it")
	require.Contains(t, d.Reason(), "one label fills each star")
}

func TestNamesHost_SeparatesConsentFromCoincidence(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		pattern string
		host    string
		want    bool
	}{
		"an exact host":        {"api.resend.com", "api.resend.com", true},
		"an address":           {"10.1.2.3", "10.1.2.3", true},
		"a star in the middle": {"email.*.amazonaws.com", "email.us-east-1.amazonaws.com", true},
		"a leading wildcard":   {"*.amazonaws.com", "s3.amazonaws.com", false},
		"a leading star with an interior one": {
			"*.s3.*.amazonaws.com", "bucket.s3.us-east-1.amazonaws.com", false,
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := engine(t, schema.ModeBlock, rule(tc.pattern, schema.ModeCapture))
			d := e.Evaluate(Request{Host: tc.host, TLS: true, Path: "/"})
			require.True(t, d.Matched(), "the fixture must match or the case says nothing")
			require.Equal(t, tc.want, d.NamesHost())
		})
	}
}

func TestNamesHost_IsFalseWhenTheDefaultDecided(t *testing.T) {
	t.Parallel()
	// Nobody named anything, so nothing may be answered on a provider's
	// behalf. The zero value has to be the safe one here.
	e := engine(t, schema.ModeCapture)
	d := e.Evaluate(Request{Host: "anything.test", TLS: true, Path: "/"})
	require.Equal(t, schema.ModeCapture, d.Mode)
	require.False(t, d.NamesHost())
}

func TestInspectsAnyHost_SeesARuleWhoseStarIsInTheMiddle(t *testing.T) {
	t.Parallel()
	// The caller used to build a probe host by stripping a leading "*." from
	// each rule and asking InspectsHost about the remainder, which answers
	// correctly for an exact host and for *.example.com and wrongly for this.
	// No certificate would have been issued, and capture would have degraded
	// to a host rule on a tunnel nothing could read inside.
	e := engine(t, schema.ModeBlock, rule("email.*.amazonaws.com", schema.ModeCapture))
	require.True(t, e.InspectsAnyHost())

	quiet := engine(t, schema.ModeBlock, rule("email.*.amazonaws.com", schema.ModeBlock))
	require.False(t, quiet.InspectsAnyHost(),
		"a policy that only blocks needs no certificate, and issuing one means every "+
			"service in the environment trusts a key nothing uses")
}
