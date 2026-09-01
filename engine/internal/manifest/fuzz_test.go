package manifest_test

import (
	"strings"
	"testing"

	"github.com/antifailure/antifailure/engine/internal/manifest"
)

// FuzzParse checks the one property a parser of untrusted input must hold: it
// never panics, and it never hangs.
//
// The manifest comes from a repository the engine did not write, so it is
// untrusted input in the same sense a network packet is. A panic here would
// crash the engine on a malformed file, and an unbounded parse would let a
// small file consume the machine.
func FuzzParse(f *testing.F) {
	seeds := []string{
		"",
		"version: 1",
		minimal,
		minimal + "\negress:\n  default: block\n",
		"version: 1\nservices: []\n",
		"version: 1\nservices:\n  - name: a\n    port: 1\n    depends_on: [a]\n",
		"{}",
		"[]",
		"null",
		"version: [1]",
		"version: {a: b}",
		"services:\n  - 1\n",
		strings.Repeat("a: &x\n  b: *x\n", 5),
		"version: 1\nname: \"\\u0000\"\n",
		"\x00\x01\x02",
		"version: 1\nservices:\n  - name: web\n    port: 99999999999999999999\n",
		"version: 1\ninvariants:\n  - name: a\n    sql: \"SELECT 1;DROP TABLE t\"\n",
		"version: 1\negress:\n  rules:\n    - host: \"*\"\n      mode: allow\n",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		// The result does not matter. Not panicking does, and so does not
		// hanging, which the fuzzer's own deadline enforces.
		m, err := manifest.Parse(data, "fuzz.yaml", "")
		if err != nil {
			return
		}
		if m == nil {
			t.Fatal("Parse returned no manifest and no error")
		}
		// A manifest that parsed must survive being explained, because the
		// dashboard renders it on every run.
		_ = manifest.Explain(m, 0)
		_ = manifest.Summary(m)
		_ = manifest.Hosts(m)

		// And it must be normalized: no later package re-applies defaults.
		if m.Egress == nil || m.Database == nil || m.Runtime == nil || m.GitHub == nil {
			t.Fatalf("Parse returned a manifest that is not normalized: %+v", m)
		}
		for _, s := range m.Services {
			if s.Build == nil || s.Resources == nil {
				t.Fatalf("service %q is not normalized", s.Name)
			}
		}
	})
}
