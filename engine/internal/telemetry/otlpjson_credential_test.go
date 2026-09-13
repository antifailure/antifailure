package telemetry

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAnExportEndpointThatDoesNotParseIsRefusedWithoutItsCredential(t *testing.T) {
	_, err := newOTLPJSONExporter("https://otel:Ot2Pass@collector.example.test:43x8/v1/traces", nil, nil)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "Ot2Pass")
	require.Contains(t, err.Error(), "invalid port", "the refusal no longer says what is wrong")
}
