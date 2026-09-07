package env

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Whether a certificate is issued at all.
//
// This used to build a probe host by stripping a leading "*." off each rule and
// asking the engine what it would do with the remainder. That answers correctly
// for an exact host and for *.example.com, and wrongly for anything else: a
// pattern with a star in the middle does not match itself, so a policy whose
// only capture rule was email.*.amazonaws.com would have been told no
// certificate was needed. The failure is silent and it is in the safe looking
// direction: nothing errors, capture simply degrades to a host rule on a tunnel
// nothing can read inside, and the mail never reaches the inbox.
func TestNeedsInspection(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		egress *schema.Egress
		want   bool
	}{
		"no policy at all": {nil, false},
		"a capture rule on an exact host": {&schema.Egress{
			Default: schema.ModeBlock,
			Rules:   []schema.EgressRule{{Host: "api.resend.com", Mode: schema.ModeCapture}},
		}, true},
		"a capture rule with a star in the middle": {&schema.Egress{
			Default: schema.ModeBlock,
			Rules:   []schema.EgressRule{{Host: "email.*.amazonaws.com", Mode: schema.ModeCapture}},
		}, true},
		"a capture rule under a leading wildcard": {&schema.Egress{
			Default: schema.ModeBlock,
			Rules:   []schema.EgressRule{{Host: "*.mailgun.net", Mode: schema.ModeCapture}},
		}, true},
		"a path rule, which cannot be applied to a tunnel": {&schema.Egress{
			Default: schema.ModeBlock,
			Rules: []schema.EgressRule{{
				Host: "api.github.com", Mode: schema.ModeAllow, Paths: []string{"/repos"},
			}},
		}, true},
		"the default is capture": {&schema.Egress{Default: schema.ModeCapture}, true},
		"nothing but blocks and allows": {&schema.Egress{
			Default: schema.ModeBlock,
			Rules: []schema.EgressRule{
				{Host: "s3.*.amazonaws.com", Mode: schema.ModeBlock},
				{Host: "*.upstash.io", Mode: schema.ModeAllow},
			},
		}, false},
		"a capture rule on another port": {&schema.Egress{
			Default: schema.ModeBlock,
			Rules:   []schema.EgressRule{{Host: "queue.test:6379", Mode: schema.ModeCapture}},
		}, false},
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, needsInspection(tc.egress))
		})
	}
}

func TestNeedsInspection_APolicyThatWillNotCompileAsksForACertificate(t *testing.T) {
	t.Parallel()
	// The conservative direction, kept deliberately. A certificate nobody uses
	// costs nothing; refusing to issue one that was needed silently downgrades
	// the policy to host rules. The manifest fails later with a better message.
	require.True(t, needsInspection(&schema.Egress{
		Rules: []schema.EgressRule{{Host: "web-*.example.com", Mode: schema.ModeBlock}},
	}))
}
