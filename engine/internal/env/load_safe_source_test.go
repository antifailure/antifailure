package env

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/load"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

func TestTrafficShapeLiteralSafeRoutes(t *testing.T) {
	for _, source := range []schema.LoadSource{"", schema.LoadNone} {
		t.Run("source_"+string(source), func(t *testing.T) {
			shape, err := loadOrchestrator(t, t.TempDir(), &schema.Load{Source: source, SafeRoutes: []string{"GET /runs"}}).trafficShape()
			require.NoError(t, err)
			require.Equal(t, []load.Route{{Method: "GET", Path: "/runs", Weight: 1}}, shape.Routes)
		})
	}
}

func TestTrafficShapeGlobFallbackStillFilters(t *testing.T) {
	shape, err := loadOrchestrator(t, t.TempDir(), &schema.Load{SafeRoutes: []string{"GET /runs/*"}}).trafficShape()
	require.NoError(t, err)
	require.Equal(t, "default", shape.Source)
	safe, _ := shape.Safe([]string{"GET /runs/*"}, nil)
	require.Empty(t, safe.Routes)
}

func TestTrafficShapeExplicitSourceWinsOverSafeRoutes(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "access.log", `1.2.3.4 - - [01/Jun/2026:12:00:00 +0000] "GET /observed HTTP/1.1" 200 12`+"\n")
	shape, err := loadOrchestrator(t, root, &schema.Load{Source: schema.LoadAccessLog, SourceConfig: map[string]string{"path": "access.log"}, SafeRoutes: []string{"GET /invented"}}).trafficShape()
	require.NoError(t, err)
	require.Equal(t, "/observed", shape.Routes[0].Path)
}
