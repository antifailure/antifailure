package personality

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestThePersonalityEngineDoesNotGateTheModelPath. BYOK is ungated on every
// edition, and the personality engine runs entirely through the same model
// path. The guardrail Vir named is that nothing here introduces a licence
// check on that path: no import of the enterprise edition, the licence, the
// feature catalogue, or the edition gate. A personality is paid for by the
// user's own key, so there is no infrastructure reason to gate it, and this
// test refuses the import that would.
//
// It reads this package's own source rather than reasoning about behavior,
// because the failure it guards is an IMPORT appearing, which a behavioral test
// would not see until something called it. Falsify it by adding an import of
// any listed path and it goes red.
func TestThePersonalityEngineDoesNotGateTheModelPath(t *testing.T) {
	forbidden := []string{
		"engine/ee/",
		"/ee/engine/",
		"engine/internal/license",
		"engine/ee/engine/license",
		"ee/engine/license",
		"ee/engine/feature",
		"engine/pkg/edition",
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	var scanned int
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		scanned++
		src, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, f := range forbidden {
			if strings.Contains(string(src), f) {
				t.Errorf("%s imports or names %q; the model path must stay ungated, so the personality "+
					"engine must not reach a licence, feature, or edition gate", name, f)
			}
		}
	}
	if scanned == 0 {
		t.Fatal("scanned no source files, so this guardrail proved nothing")
	}
}
