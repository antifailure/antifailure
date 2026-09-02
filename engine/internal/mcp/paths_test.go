package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// checkout builds a repository root with one ordinary file in it, plus a
// secret outside the root for the escape tests to aim at.
func checkout(t *testing.T) (root, outside string) {
	t.Helper()
	base := t.TempDir()
	root = filepath.Join(base, "repo")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "migrations"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "migrations", "001_add_index.sql"),
		[]byte("CREATE INDEX CONCURRENTLY ON orders (customer_id);\n"), 0o644))

	outside = filepath.Join(base, "secret.txt")
	require.NoError(t, os.WriteFile(outside, []byte("not yours\n"), 0o600))
	return root, outside
}

func TestResolveInRoot_ReadsAndHashesAnOrdinaryFile(t *testing.T) {
	t.Parallel()
	root, _ := checkout(t)

	got, fault := resolveInRoot(root, "migrations/001_add_index.sql")
	require.Nil(t, fault)
	require.Equal(t, "migrations/001_add_index.sql", got.Rel)

	want := sha256.Sum256([]byte("CREATE INDEX CONCURRENTLY ON orders (customer_id);\n"))
	require.Equal(t, hex.EncodeToString(want[:]), got.SHA256,
		"the hash covers the exact bytes that were read")
	require.Equal(t, int64(len(got.Bytes)), got.Size)
}

func TestResolveInRoot_RejectsTraversal(t *testing.T) {
	t.Parallel()
	root, _ := checkout(t)

	for _, rel := range []string{
		"../secret.txt",
		"../../etc/passwd",
		"migrations/../../secret.txt",
		"./../secret.txt",
		"migrations/./../../secret.txt",
	} {
		_, fault := resolveInRoot(root, rel)
		require.NotNil(t, fault, "the path %q must be refused", rel)
		require.Equal(t, FaultPathRejected, fault.Code, "path %q", rel)
	}
}

func TestResolveInRoot_RejectsAbsolutePaths(t *testing.T) {
	t.Parallel()
	root, outside := checkout(t)

	_, fault := resolveInRoot(root, outside)
	require.NotNil(t, fault)
	require.Equal(t, FaultPathRejected, fault.Code)

	_, fault = resolveInRoot(root, "/etc/passwd")
	require.NotNil(t, fault)
	require.Equal(t, FaultPathRejected, fault.Code)
}

func TestResolveInRoot_RejectsASymlinkLeavingTheCheckout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege on Windows that CI does not grant")
	}
	t.Parallel()
	root, outside := checkout(t)

	// The dangerous case, and the one a textual check on the unresolved path
	// cannot see: the path contains no parent reference at all and still
	// lands outside the tree. Only comparing the RESOLVED paths catches it.
	link := filepath.Join(root, "migrations", "innocent.sql")
	require.NoError(t, os.Symlink(outside, link))

	_, fault := resolveInRoot(root, "migrations/innocent.sql")
	require.NotNil(t, fault)
	require.Equal(t, FaultPathRejected, fault.Code)
	require.NotContains(t, fault.Detail, outside,
		"the refusal does not confirm the resolved host path back to the caller")
}

func TestResolveInRoot_RejectsASymlinkedParentDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege on Windows that CI does not grant")
	}
	t.Parallel()
	root, outside := checkout(t)

	// The escape is a directory component rather than the leaf, which is the
	// variant a check that only inspects the final element would miss.
	require.NoError(t, os.Symlink(filepath.Dir(outside), filepath.Join(root, "elsewhere")))

	_, fault := resolveInRoot(root, "elsewhere/secret.txt")
	require.NotNil(t, fault)
	require.Equal(t, FaultPathRejected, fault.Code)
}

func TestResolveInRoot_AllowsASymlinkThatStaysInside(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege on Windows that CI does not grant")
	}
	t.Parallel()
	root, _ := checkout(t)

	// Not everything with a symlink in it is an attack. A repository that
	// links one migration to another is ordinary, and refusing it would make
	// the check something people route around.
	require.NoError(t, os.Symlink(
		filepath.Join(root, "migrations", "001_add_index.sql"),
		filepath.Join(root, "latest.sql")))

	got, fault := resolveInRoot(root, "latest.sql")
	require.Nil(t, fault)
	require.Contains(t, string(got.Bytes), "CREATE INDEX")
}

func TestResolveInRoot_RejectsADirectory(t *testing.T) {
	t.Parallel()
	root, _ := checkout(t)

	_, fault := resolveInRoot(root, "migrations")
	require.NotNil(t, fault)
	require.Equal(t, FaultPathRejected, fault.Code)
	require.Contains(t, fault.Detail, "regular file")
}

func TestResolveInRoot_RejectsADeviceFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("device paths differ on Windows")
	}
	t.Parallel()
	root, _ := checkout(t)

	// /dev/zero would read forever. It is reached through a symlink because
	// an absolute path is refused earlier, and the point is that the device
	// is refused on its own merits rather than only by the path check.
	require.NoError(t, os.Symlink("/dev/zero", filepath.Join(root, "zero")))

	_, fault := resolveInRoot(root, "zero")
	require.NotNil(t, fault)
	require.Equal(t, FaultPathRejected, fault.Code)
}

func TestResolveInRoot_RejectsAMissingFile(t *testing.T) {
	t.Parallel()
	root, _ := checkout(t)

	_, fault := resolveInRoot(root, "migrations/nope.sql")
	require.NotNil(t, fault)
	require.Equal(t, FaultPathRejected, fault.Code)
}

func TestResolveInRoot_RejectsNullBytesAndEmptyPaths(t *testing.T) {
	t.Parallel()
	root, _ := checkout(t)

	_, fault := resolveInRoot(root, "")
	require.NotNil(t, fault)

	// A NUL truncates the name at the system call boundary, so the string the
	// checks above inspected is not the string the kernel would open.
	_, fault = resolveInRoot(root, "migrations/001_add_index.sql\x00.txt")
	require.NotNil(t, fault)
	require.Equal(t, FaultPathRejected, fault.Code)
}

func TestResolveInRoot_RejectsAnOversizedFile(t *testing.T) {
	t.Parallel()
	root, _ := checkout(t)

	big := filepath.Join(root, "big.sql")
	require.NoError(t, os.WriteFile(big, make([]byte, maxRepositoryFileBytes+1), 0o644))

	_, fault := resolveInRoot(root, "big.sql")
	require.NotNil(t, fault)
	require.Equal(t, FaultArgumentTooLarge, fault.Code)
}

func TestWithin_DoesNotTreatASiblingAsAChild(t *testing.T) {
	t.Parallel()
	// "/repo" is a string prefix of "/repo-backup" and is not its parent.
	// This is the whole bug the separator boundary exists to not have.
	sep := string(filepath.Separator)
	require.False(t, within(sep+"repo", sep+"repo-backup"+sep+"x"))
	require.True(t, within(sep+"repo", sep+"repo"+sep+"x"))
	require.True(t, within(sep+"repo", sep+"repo"))
}

func TestResolveInRoot_HashesTheBytesItActuallyRead(t *testing.T) {
	t.Parallel()
	root, _ := checkout(t)

	// The file is replaced between two resolutions. The property is that each
	// result's hash matches that result's own bytes: a hash taken from a
	// separate read could describe a file that is no longer the one used.
	first, fault := resolveInRoot(root, "migrations/001_add_index.sql")
	require.Nil(t, fault)

	require.NoError(t, os.WriteFile(
		filepath.Join(root, "migrations", "001_add_index.sql"),
		[]byte("DROP TABLE orders;\n"), 0o644))

	second, fault := resolveInRoot(root, "migrations/001_add_index.sql")
	require.Nil(t, fault)

	require.NotEqual(t, first.SHA256, second.SHA256, "the swap was noticed")
	for _, got := range []*CheckedFile{first, second} {
		sum := sha256.Sum256(got.Bytes)
		require.Equal(t, hex.EncodeToString(sum[:]), got.SHA256,
			"the hash describes the bytes carried alongside it")
	}
}
