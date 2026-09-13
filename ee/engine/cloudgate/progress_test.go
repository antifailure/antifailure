// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package cloudgate_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/cloudgate"
	"github.com/antifailure/antifailure/engine/pkg/extension"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

type reportingDatabase struct {
	*fakeDatabase
	report func(string)
}

func (d *reportingDatabase) ReportProgressTo(report func(string)) { d.report = report }

type reportingDatabaseProvider struct{ db *reportingDatabase }

func (p *reportingDatabaseProvider) Name() string { return "aurora" }

func (p *reportingDatabaseProvider) Open(context.Context, extension.DatabaseConfig) (provider.Database, error) {
	return p.db, nil
}

func TestTheGateForwardsProgressReportingToTheProviderUnderneath(t *testing.T) {
	t.Parallel()
	inner := &reportingDatabase{fakeDatabase: &fakeDatabase{rec: &recorder{}}}
	reg := extension.NewRegistry()
	reg.AddDatabaseProvider(&reportingDatabaseProvider{db: inner})
	require.Equal(t, 1, cloudgate.Wrap(reg))

	p, ok := reg.DatabaseProviderNamed("aurora")
	require.True(t, ok)
	gated, err := p.Open(context.Background(), extension.DatabaseConfig{})
	require.NoError(t, err)

	reporting, ok := gated.(provider.ProgressReporting)
	require.True(t, ok, "the gated database does not offer ReportProgressTo, so the engine cannot hand a wrapped provider its progress sink")

	var got []string
	reporting.ReportProgressTo(func(line string) { got = append(got, line) })
	require.NotNil(t, inner.report, "the gate accepted a progress sink and did not pass it to the provider underneath")
	inner.report("waiting for a first backup")
	require.Equal(t, []string{"waiting for a first backup"}, got)
}
