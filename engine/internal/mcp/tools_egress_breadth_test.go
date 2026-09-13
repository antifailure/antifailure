package mcp

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/policy"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// An agent asking whether a catch hook is safe to call reads this tool, and
// allowed: true for *.zapier.com used to be the whole of the answer.
func broadEngine(t *testing.T) *policy.Engine {
	t.Helper()
	eng, err := policy.New(&schema.Egress{
		Default: schema.ModeBlock,
		Rules:   []schema.EgressRule{{Host: "*.zapier.com", Mode: schema.ModeAllow}},
	})
	require.NoError(t, err)
	return eng
}

func TestProbe_ARuleThatNamesNoHostSaysHowFarItReaches(t *testing.T) {
	t.Parallel()
	out, fault := runProbes(broadEngine(t), map[string]any{"probe": []any{
		map[string]any{"method": "POST", "url": "https://hooks.zapier.com/hooks/catch/1234/abcd"},
	}})
	require.Nil(t, fault)
	require.Len(t, out, 1)
	require.True(t, out[0].Allowed, "the fixture must be let out or the caution says nothing")
	require.Contains(t, out[0].Caution, "every name under zapier.com")
}

func TestProbe_APatternIsNotARequest(t *testing.T) {
	t.Parallel()
	out, fault := runProbes(broadEngine(t), map[string]any{"probe": []any{
		map[string]any{"method": "GET", "url": "https://*.zapier.com/"},
	}})
	require.Nil(t, fault)
	require.Len(t, out, 1)
	require.NotEmpty(t, out[0].Error)
	require.Empty(t, out[0].Mode, "a pattern must not be answered as though it were a host")
}

func TestDescribePolicy_ARuleThatNamesNoHostSaysHowFarItReaches(t *testing.T) {
	t.Parallel()
	doc := describePolicy(broadEngine(t))
	require.Len(t, doc.Rules, 1)
	require.Contains(t, doc.Rules[0].Caution, "every name under zapier.com")
}
