package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/antifailure/antifailure/engine/internal/cli"
)

func TestDoctorLifecycleChecksReachTheCommand(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "antifailure.yaml"), []byte("version: invalid\n"), 0600); err != nil {
		t.Fatal(err)
	}
	result := runCLI(t, dir, nil, "doctor", "-o", "json")
	var report cli.DoctorReport
	if err := json.Unmarshal([]byte(result.stdout), &report); err != nil {
		t.Fatal(err)
	}
	found := map[string]cli.CheckResult{}
	for _, check := range report.Checks {
		found[check.Name] = check
	}
	if _, ok := found["CLI version"]; !ok {
		t.Fatal("doctor did not inspect its installed version")
	}
	if check, ok := found["Project manifest"]; !ok || check.Status != cli.CheckFail {
		t.Fatal("doctor did not reject the invalid manifest")
	}
	if report.OK {
		t.Fatal("invalid project was reported ready")
	}
	if result.code == 0 {
		t.Fatal("invalid project exited successfully")
	}
}
