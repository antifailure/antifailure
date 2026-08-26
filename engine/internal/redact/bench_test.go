package redact_test

import (
	"testing"

	"github.com/antifailure/antifailure/engine/internal/redact"
)

// Redaction sits on the write path of every log line and every event the
// engine produces, so a slow redactor is a slow engine. The budget is two
// microseconds per line for a line with no match, which is the overwhelming
// majority of lines.
func BenchmarkRedactor_NoMatch(b *testing.B) {
	r := redact.New()
	for i := 0; i < 32; i++ {
		r.Register("registered-secret-value-number-" + string(rune('a'+i)))
	}
	const line = `level=info ts=2026-08-25T12:00:00Z env=env_a1b2c3d4e5f6g7h8 ` +
		`msg="branch created" provider=docker golden=gv_20260825120000_deadbeef dur_ms=812`
	b.ReportAllocs()
	b.SetBytes(int64(len(line)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if out := r.String(line); len(out) == 0 {
			b.Fatal("empty")
		}
	}
}

func BenchmarkRedactor_WithMatch(b *testing.B) {
	r := redact.New()
	const line = `level=error msg="provider call failed" url=postgres://app:s3cretpassw0rd@db:5432/main ` +
		`auth="Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.abcdefghijklmnopqrstuvwxyz012345"`
	b.ReportAllocs()
	b.SetBytes(int64(len(line)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if out := r.String(line); len(out) == 0 {
			b.Fatal("empty")
		}
	}
}
