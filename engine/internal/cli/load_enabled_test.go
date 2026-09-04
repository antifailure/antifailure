package cli

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

func TestCILoadEnabled(t *testing.T) {
	for _, tc := range []struct {
		name        string
		manifest    *schema.Manifest
		force, want bool
	}{
		{"nil", nil, false, false},
		{"absent", &schema.Manifest{}, false, false},
		{"disabled", &schema.Manifest{Load: &schema.Load{}}, false, false},
		{"enabled", &schema.Manifest{Load: &schema.Load{Enabled: true}}, false, true},
		{"forced absent", &schema.Manifest{}, true, true},
		{"forced disabled", &schema.Manifest{Load: &schema.Load{}}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) { require.Equal(t, tc.want, shouldRunLoad(tc.manifest, tc.force)) })
	}
}
