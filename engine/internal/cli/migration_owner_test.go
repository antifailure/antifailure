package cli_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func migrationOwnerFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "dashboard"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"dashboard/package.json": `{"name":"dashboard","dependencies":{"next":"15"}}`,
		"alembic.ini":            "[alembic]\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestMigrationOwner_UnattendedRunRefusesToGuess(t *testing.T) {
	dir := migrationOwnerFixture(t)
	got := runCLI(t, dir, nil, "init", "--non-interactive")
	require.Contains(t, got.stderr, "AF-DET-004")
}

func TestMigrationOwner_AnswerReachesWrittenManifest(t *testing.T) {
	dir := migrationOwnerFixture(t)
	got := runCLI(t, dir, nil, "init", "--non-interactive", "--answer", "migration."+filepath.Base(dir)+".service=dashboard")
	if got.code != 0 {
		t.Fatal(got.stderr)
	}
	body, err := os.ReadFile(filepath.Join(dir, "antifailure.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	require.Contains(t, string(body), "migrate: alembic upgrade head")
}

func TestMigrationOwner_ManualChoiceDisclosesMissingSetup(t *testing.T) {
	dir := migrationOwnerFixture(t)
	got := runCLI(t, dir, nil, "init", "--non-interactive", "--answer", "migration."+filepath.Base(dir)+".service=manual:configure")
	if got.code != 0 {
		t.Fatal(got.stderr)
	}
	require.Contains(t, got.stdout, "Not configured: alembic upgrade head")
}

func TestMigrationOwner_UnknownOwnerIsRefused(t *testing.T) {
	dir := migrationOwnerFixture(t)
	got := runCLI(t, dir, nil, "init", "--non-interactive", "--answer", "migration."+filepath.Base(dir)+".service=absent")
	require.Contains(t, got.stderr, "AF-DET-006")
}

func TestMigrationOwner_ManualChoiceRemainsInTheManifest(t *testing.T) {
	dir := migrationOwnerFixture(t)
	got := runCLI(t, dir, nil, "init", "--non-interactive", "--answer", "migration."+filepath.Base(dir)+".service=manual:configure")
	if got.code != 0 {
		t.Fatal(got.stderr)
	}
	body, err := os.ReadFile(filepath.Join(dir, "antifailure.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	require.Contains(t, string(body), "# Not configured: alembic upgrade head")
}
