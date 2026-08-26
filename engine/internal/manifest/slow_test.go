package manifest_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/antifailure/antifailure/engine/internal/manifest"
)

// TestParse_NoCorpusInputIsPathologicallySlow scans the fuzz corpus and fails
// on any input that takes long enough to be a denial of service.
//
// A parser of untrusted input has two failure modes, and not panicking is only
// the first. The second is an input that parses correctly and takes a minute
// doing it, which is just as effective at stopping an engine. The fuzzer finds
// those but reports them only as a stalled execution rate, so this test turns
// that into an assertion.
func TestParse_NoCorpusInputIsPathologicallySlow(t *testing.T) {
	t.Parallel()
	const budget = 250 * time.Millisecond

	dirs := corpusDirs(t)
	type result struct {
		name string
		took time.Duration
		size int
	}
	var results []result
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			body, ok := decodeCorpusEntry(raw)
			if !ok {
				continue
			}
			start := time.Now()
			_, _ = manifest.Parse(body, "corpus.yaml", "")
			results = append(results, result{e.Name(), time.Since(start), len(body)})
		}
	}
	if len(results) == 0 {
		t.Skip("no fuzz corpus on this machine yet")
	}
	sort.Slice(results, func(i, j int) bool { return results[i].took > results[j].took })

	t.Logf("scanned %d corpus inputs, slowest %s (%d bytes)",
		len(results), results[0].took, results[0].size)
	if results[0].took > budget {
		var b strings.Builder
		for i := 0; i < 5 && i < len(results); i++ {
			b.WriteString(results[i].name + " " + results[i].took.String() + "\n")
		}
		t.Fatalf("a corpus input parses slower than %s:\n%s", budget, b.String())
	}
}

func corpusDirs(t *testing.T) []string {
	t.Helper()
	var dirs []string
	if local := filepath.Join("testdata", "fuzz", "FuzzParse"); dirExists(local) {
		dirs = append(dirs, local)
	}
	out, err := exec.Command("go", "env", "GOCACHE").Output()
	if err == nil {
		cache := strings.TrimSpace(string(out))
		shared := filepath.Join(cache, "fuzz",
			"github.com/antifailure/antifailure/engine/internal/manifest", "FuzzParse")
		if dirExists(shared) {
			dirs = append(dirs, shared)
		}
	}
	return dirs
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// decodeCorpusEntry reads Go's fuzz corpus format: a version line followed by
// one quoted byte slice literal per argument.
func decodeCorpusEntry(raw []byte) ([]byte, bool) {
	for _, l := range strings.Split(string(raw), "\n") {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, "[]byte(") || !strings.HasSuffix(l, ")") {
			continue
		}
		s, err := strconv.Unquote(strings.TrimSuffix(strings.TrimPrefix(l, "[]byte("), ")"))
		if err != nil {
			return nil, false
		}
		return []byte(s), true
	}
	return nil, false
}
