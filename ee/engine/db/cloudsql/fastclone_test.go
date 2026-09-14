// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package cloudsql_test

// The claim this provider exists to make, asserted rather than described.
//
// Capabilities reports CopyOnWrite true. On Cloud SQL that is only true of the
// FAST clone workflow, Cloud SQL selects between fast and standard from the
// shape of the request, and it returns the same operation either way. So the
// capability is a claim about the bytes this provider puts on the wire, and
// these are the tests that hold it to that.
//
// Two independent instruments, because neither covers the other:
//
//   - TestTheCloneRequestCannotAskForTheSlowPath reads the MARSHALLED request.
//     It is a claim about the JSON, and it would still fail if the fake stopped
//     modelling the rule.
//   - TestNoOperationEverCausesAStandardClone reads the fake's counters across
//     a whole lifecycle. It is a claim about every call site, and it would
//     catch a second clone path added somewhere this file does not name.
//
// The first would pass if some other code path built its own request. The
// second would pass if the fake's rule drifted from Google's. Together they say
// the provider builds only the fast shape AND that every clone it causes is
// counted fast by the documented rule.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// seedSQL is a table with rows in it, so that a clone has bytes to move.
const seedSQL = `
CREATE TABLE people (id serial PRIMARY KEY, name text NOT NULL);
INSERT INTO people (name) SELECT 'person ' || g FROM generate_series(1, 500) g;
`

// TestTheCloneRequestCannotAskForTheSlowPath asserts on the bytes.
//
// It marshals the request the provider builds and requires that the three
// fields which force Cloud SQL's standard workflow are ABSENT from the JSON,
// not merely empty. Absent matters: Google's API distinguishes a field that is
// not there from one that is there and empty, and preferredZone set to the
// source's OWN zone is documented as forcing the standard path.
//
// Asserting on the marshalled bytes rather than on the Go value is the point.
// The Go value is what this module controls; the bytes are what Google reads.
func TestTheCloneRequestCannotAskForTheSlowPath(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(cloneRequestForTest("af-b-destination"))
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	ctx, ok := decoded["cloneContext"].(map[string]any)
	require.True(t, ok, "the request has no cloneContext at all: %s", encoded)

	require.Equal(t, "af-b-destination", ctx["destinationInstanceName"],
		"the clone would land on the wrong instance")

	for _, forbidden := range []string{"preferredZone", "pointInTime"} {
		_, present := ctx[forbidden]
		require.Falsef(t, present,
			"cloneContext carries %q, which Google documents as forcing the STANDARD "+
				"clone workflow. The standard workflow takes a full backup and provisions "+
				"from it, so its duration scales with the size of the database, and "+
				"Capabilities() would be reporting CopyOnWrite true about a request that "+
				"is not copy on write. preferredZone is the one to look at twice: "+
				"re-specifying even the SOURCE'S OWN zone falls back to the standard "+
				"path, so the careful looking request is the broken one. Request was: %s",
			forbidden, encoded)
	}
}

// TestNoOperationEverCausesAStandardClone drives a whole lifecycle and reads
// the fake's counters.
//
// The fake applies Google's own rule to every clone it serves and counts fast
// and standard separately. So this is not "the provider says it is fast", it is
// "every clone this provider caused was classified fast by the documented
// rule", across refresh and branch together.
//
// The zero assertion on StandardClones is the load bearing one and it is
// separate from the positive assertion on purpose. A provider that stopped
// cloning altogether would leave both counters at zero, so requiring the fast
// count to be POSITIVE is what stops this test passing by finding nothing.
func TestNoOperationEverCausesAStandardClone(t *testing.T) {
	server := newFake(t, seedSQL)
	p := newProvider(t, server)
	ctx := context.Background()

	version, err := p.RefreshGolden(ctx, goldenSpec())
	require.NoError(t, err)
	_, err = p.Branch(ctx, version.ID, "env_fast_clone")
	require.NoError(t, err)

	require.Positive(t, server.FastClones(),
		"this lifecycle issued no fast clone at all, so the assertion below that it "+
			"issued no slow one is satisfied by there being no clone to classify")
	require.Zero(t, server.StandardClones(),
		"this provider caused %d STANDARD clones. Cloud SQL picks the standard "+
			"workflow when a clone names a zone or carries a point in time, it says "+
			"nothing about which it chose, and its duration scales with the size of the "+
			"database. Capabilities() reports CopyOnWrite true, so a standard clone here "+
			"makes that a false claim rather than a slow one", server.StandardClones())
}

// TestTheFakeWouldNoticeASlowClone is the positive control for the test above.
//
// Without it, TestNoOperationEverCausesAStandardClone is a check that cannot
// say no: a fake whose StandardClones counter never incremented under any
// circumstances would report zero forever and the assertion would hold while
// measuring nothing. This drives a request that Google's rule classifies as
// standard and requires the counter to move.
func TestTheFakeWouldNoticeASlowClone(t *testing.T) {
	server := newFake(t, seedSQL)
	ctx := context.Background()

	require.Zero(t, server.StandardClones())
	require.NoError(t, postSlowClone(ctx, server.URL(), testProject, sourceInstance, "af-slow-clone"))
	require.Equal(t, 1, server.StandardClones(),
		"the fake served a clone naming a zone and did not count it as standard, so "+
			"the zero assertion in TestNoOperationEverCausesAStandardClone is measuring "+
			"nothing and would stay green if this provider started asking for slow clones")
	require.Zero(t, server.FastClones(),
		"the fake counted a zone naming clone as fast, so its rule does not match "+
			"Google's and the counters cannot be trusted in either direction")
}
