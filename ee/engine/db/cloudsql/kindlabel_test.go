// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package cloudsql_test

// A branch must never be mistaken for a golden, whatever Cloud SQL does with a
// clone's labels.
//
// THE DEFECT. On Azure a branch is a restore of a golden, and the restore
// carried the golden's identifying tags. The branch was then listed as a second
// golden, and the check that stops DestroyGolden removing a golden with live
// branches skipped it, because it looked for branches among instances that did
// not look like goldens. The Azure fix, like the Aurora provider before it
// (ee/engine/db/aurora/aurora.go, tagKind "antifailure:kind" with the values
// candidate, golden and branch), records what an instance IS in one explicit
// kind label rather than inferring it from which metadata happens to be
// present.
//
// Cloud SQL decided "golden" from whether the golden label was non-empty, so
// its correctness depended on whether Google copies user labels onto a clone.
// That has NOT been established. So these tests run the provider against both
// answers a fake can give, and neither is allowed to matter:
//
//   - request wins: a clone copies its source's labels and a later label write
//     replaces them. The branch must list as a branch, carry none of the
//     golden's identifying labels, and hold its golden against DestroyGolden.
//   - inherited wins: a clone copies its source's labels and a later label
//     write cannot change them, while still answering success. The provider
//     must FAIL CLOSED.
//
// Fail closed means three things here, each one a place the Azure defect
// reached. Branch returns an error and removes the clone rather than handing an
// environment an instance whose labels say golden. No listing reports any
// instance other than the published golden as a golden. And every check that
// counts or protects branches treats an owned instance that is not provably a
// golden as a branch, so neither DestroyGolden's refusal nor the branch limit
// can be skipped by an instance that merely inherited a golden's labels.
//
// Both tests also model a worker killed straight after its clone was accepted,
// before any label write. That instance carries every label its golden had
// under either answer, so it is the purest form of the defect: nothing the
// provider wrote can rescue it, only what the provider reads.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/db/cloudsql"
	"github.com/antifailure/antifailure/ee/engine/db/cloudsql/fakecloudsql"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

func TestKindLabel_RequestWins_ABranchIsNeverListedAsAGolden(t *testing.T) {
	ctx := context.Background()
	server := newLabelInheritingFake(t, fakecloudsql.InheritedLabelsYield)
	p := newProvider(t, server)

	golden, err := p.RefreshGolden(ctx, goldenSpec())
	require.NoError(t, err)
	branch, err := p.Branch(ctx, golden.ID, "kind-request-wins")
	require.NoError(t, err, "Branch must succeed when a label write overrides what the clone inherited")

	goldens, err := p.ListGoldens(ctx)
	require.NoError(t, err)
	require.Len(t, goldens, 1, "a branch that inherited its golden's labels was listed as a second golden")
	require.Equal(t, golden.ID, goldens[0].ID)
	require.Equal(t, golden.ProviderRef, goldens[0].ProviderRef)

	inventory := inventoryByID(t, p)
	require.Equal(t, "golden", inventory[golden.ProviderRef].Kind)
	require.Equal(t, "branch", inventory[branch.ProviderRef].Kind, "Inventory reported the branch as something other than a branch")
	require.Empty(t, goldenOnlyLabels(inventory[branch.ProviderRef].Labels),
		"the branch still carries labels only a golden should carry, so anything reading them would take it for one")

	err = p.DestroyGolden(ctx, golden.ID)
	require.ErrorContains(t, err, "AF-DB-005", "a golden with a live branch was destroyable")

	leftover := killedWorkerClone(ctx, t, server, golden.ProviderRef, "kind-request-wins-killed")
	goldens, err = p.ListGoldens(ctx)
	require.NoError(t, err)
	require.Len(t, goldens, 1, "a clone left by a killed worker, carrying every label of its golden, was listed as a golden")
	inventory = inventoryByID(t, p)
	require.Contains(t, inventory, leftover, "the killed worker's clone is invisible to Inventory")
	require.NotEqual(t, "golden", inventory[leftover].Kind, "Inventory reported the killed worker's clone as a golden")

	require.NoError(t, p.Destroy(ctx, branch))
	err = p.DestroyGolden(ctx, golden.ID)
	require.ErrorContains(t, err, "AF-DB-005", "the golden was destroyable while a clone of it still existed")
	require.ErrorContains(t, err, leftover)
}

func TestKindLabel_InheritedWins_BranchFailsClosed(t *testing.T) {
	ctx := context.Background()
	server := newLabelInheritingFake(t, fakecloudsql.InheritedLabelsWin)
	p := newProvider(t, server)

	golden, err := p.RefreshGolden(ctx, goldenSpec())
	require.NoError(t, err)
	before := server.ResourceCount()

	_, err = p.Branch(ctx, golden.ID, "kind-inherited-wins")
	require.Error(t, err, "Branch handed out an instance whose label write did not take")
	require.ErrorContains(t, err, "antifailure-kind", "the refusal did not name the kind label the clone kept")
	require.Equal(t, before, server.ResourceCount(), "the refused clone was left behind")

	goldens, err := p.ListGoldens(ctx)
	require.NoError(t, err)
	require.Len(t, goldens, 1, "an instance other than the published golden was listed as a golden")
	require.Equal(t, golden.ProviderRef, goldens[0].ProviderRef)
	for id, resource := range inventoryByID(t, p) {
		if id != golden.ProviderRef {
			require.NotEqual(t, "golden", resource.Kind, "Inventory reported %s as a golden", id)
		}
	}

	leftover := killedWorkerClone(ctx, t, server, golden.ProviderRef, "kind-inherited-wins-killed")
	goldens, err = p.ListGoldens(ctx)
	require.NoError(t, err)
	require.Len(t, goldens, 1, "a clone left by a killed worker, carrying every label of its golden, was listed as a golden")
	inventory := inventoryByID(t, p)
	require.Contains(t, inventory, leftover, "the killed worker's clone is invisible to Inventory")
	require.NotEqual(t, "golden", inventory[leftover].Kind, "Inventory reported the killed worker's clone as a golden")
	err = p.DestroyGolden(ctx, golden.ID)
	require.ErrorContains(t, err, "AF-DB-005", "the golden was destroyable while a clone of it still existed")
}

// newLabelInheritingFake is newFake with one answer to the label question.
func newLabelInheritingFake(t *testing.T, inheritance fakecloudsql.LabelInheritance) *fakecloudsql.Server {
	t.Helper()
	server, err := fakecloudsql.New(fakecloudsql.Options{
		AdminURL:         requirePostgres(t),
		Prefix:           "af_cs_" + randomSuffix(t) + "_",
		Project:          testProject,
		SourceInstance:   sourceInstance,
		LabelInheritance: inheritance,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		for _, problem := range server.Close() {
			t.Errorf("the fake control plane could not clean up: %v", problem)
		}
	})
	return server
}

// killedWorkerClone sends the provider's own clone request for a branch name
// and then does nothing else, which is what a worker killed straight after the
// clone was accepted leaves behind.
func killedWorkerClone(ctx context.Context, t *testing.T, server *fakecloudsql.Server, goldenInstance, envID string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(testProject + "\x00" + sourceInstance + "\x00" + envID))
	name := "af-b-" + hex.EncodeToString(sum[:])[:16]
	body, err := json.Marshal(cloudsql.CloneRequestForTest(name))
	require.NoError(t, err)
	url := fmt.Sprintf("%s/v1/projects/%s/instances/%s/clone", server.URL(), testProject, goldenInstance)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer AF_FAKE_CLOUDSQL_TOKEN")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode, "the fake refused the killed worker's clone")
	return name
}

func inventoryByID(t *testing.T, p *cloudsql.Provider) map[string]provider.Resource {
	t.Helper()
	resources, err := p.Inventory(context.Background())
	require.NoError(t, err)
	out := map[string]provider.Resource{}
	for _, r := range resources {
		out[r.ID] = r
	}
	return out
}

// goldenOnlyLabels names the labels with a value that only a golden should
// carry: its marker and the chunked rules hash, provenance and attestation.
// The keys are spelled out rather than imported, so a rename in the provider
// cannot quietly make this check look at nothing.
func goldenOnlyLabels(labels map[string]string) []string {
	var out []string
	for key, value := range labels {
		if value == "" {
			continue
		}
		if key == "antifailure-golden" || strings.HasPrefix(key, "af-rules-") ||
			strings.HasPrefix(key, "af-prov-") || strings.HasPrefix(key, "af-att-") {
			out = append(out, key)
		}
	}
	return out
}
