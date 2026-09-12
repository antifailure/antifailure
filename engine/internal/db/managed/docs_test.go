package managed

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The two tables that would have drifted.
//
// The registry decides what the engine does. The documentation page decides
// what a buyer believes. They carry the same thirteen verdicts written twice,
// and nothing connected them, so a vendor whose product changed could have been
// corrected in one and left in the other, and the wrong half is the half a
// person reads before deciding whether to try this at all.
//
// The alternative was generating the page from the registry, which is what this
// repository does for the CLI reference and the error catalogue. It was not
// done here because the page is mostly prose: three sections of reasoning, a
// measurement table, and an explanation of why one vendor got no provider.
// Generating it would have meant putting that prose in Go string literals,
// which is where documentation goes to stop being edited. So the page stays
// hand written and this test is the join, which is the same guarantee bought a
// different way and it is bought only if the test can actually say no. It can:
// the mutation table for this lane breaks the table and the registry in turn
// and records that each break turns it red.
//
// It reads the vendor column and the copy on write column and the host server
// column. It does NOT read the prose, and that limit is worth stating: a
// paragraph that contradicts a verdict is not caught here, and nothing short of
// a person reading it would catch that.

// docsPage is the page this test joins to the registry.
func docsPage(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	// engine/internal/db/managed -> the repository root.
	root := filepath.Join(filepath.Dir(file), "..", "..", "..", "..")
	path := filepath.Join(root, "docs", "src", "content", "docs", "providers", "managed-postgres.md")
	b, err := os.ReadFile(path)
	require.NoError(t, err,
		"the page this registry is published as is missing, and a registry nobody "+
			"publishes is a decision nobody can read")
	return string(b)
}

// vendorRows returns the rows of the page's vendor table, keyed by the first
// column, which is the vendor's Display.
//
// The table is found by its header rather than by position, because a page that
// grew a second table would otherwise be read by counting and the count would
// be wrong in a way that still parses.
func vendorRows(t *testing.T, page string) map[string][]string {
	t.Helper()
	const header = "| Vendor | Its own mechanism | CoW | Can host goldens | Read on |"
	i := strings.Index(page, header)
	require.GreaterOrEqual(t, i, 0,
		"the vendor table's header is not on the page in the shape this test reads. "+
			"A header that was reworded silently turns this check into one that "+
			"examines nothing, so it is refused rather than skipped.")
	rows := map[string][]string{}
	for _, line := range strings.Split(page[i:], "\n") {
		if !strings.HasPrefix(line, "| ") {
			break
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		for k := range cells {
			cells[k] = strings.TrimSpace(cells[k])
		}
		if cells[0] == "Vendor" || strings.HasPrefix(cells[0], "---") {
			continue
		}
		rows[cells[0]] = cells
	}
	return rows
}

func TestTheDocumentedTableCarriesEveryVendorInTheRegistry(t *testing.T) {
	rows := vendorRows(t, docsPage(t))

	var documented, registered []string
	for name := range rows {
		documented = append(documented, name)
	}
	for _, v := range All() {
		registered = append(registered, v.Display)
	}
	sort.Strings(documented)
	sort.Strings(registered)
	require.Equal(t, registered, documented,
		"the page and the registry hold the same thirteen verdicts written twice, and "+
			"a vendor in one and not the other is the half a reader trusts being the "+
			"half nobody updated")
}

func TestTheDocumentedCopyOnWriteColumnMatchesTheRegistry(t *testing.T) {
	rows := vendorRows(t, docsPage(t))
	for _, v := range All() {
		cells, ok := rows[v.Display]
		require.True(t, ok, "%s is not on the page", v.Display)
		// Bold markers stripped: the page emphasises the two answers a reader
		// should not skim past, and emphasis is not a different answer.
		cow := strings.ToLower(strings.ReplaceAll(cells[2], "*", ""))
		require.Equal(t, v.CopyOnWrite, cow == "yes",
			"%s declares CopyOnWrite=%v in the registry and the page's copy on write "+
				"column reads %q. Copy on write is the distinguishing commercial claim of "+
				"this whole wave, so the one column a buyer reads it from cannot be the "+
				"one nobody joined to the code.", v.Display, v.CopyOnWrite, cells[2])
	}
}

func TestTheDocumentedHostServerColumnMatchesTheRegistry(t *testing.T) {
	rows := vendorRows(t, docsPage(t))
	for _, v := range All() {
		cells, ok := rows[v.Display]
		require.True(t, ok, "%s is not on the page", v.Display)
		cell := strings.ToLower(strings.ReplaceAll(cells[3], "*", ""))
		require.True(t, strings.HasPrefix(cell, string(v.HostServer.Answer)),
			"%s answers %q to the host server question in the registry and the page's "+
				"column begins %q. That column is what decides whether somebody points "+
				"PGURL_ADMIN_URL at their vendor, and the engine refuses on the registry's "+
				"answer rather than on the page's.",
			v.Display, v.HostServer.Answer, cells[3])
	}
}
