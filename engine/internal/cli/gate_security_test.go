package cli

import (
	"testing"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/report"
)

// A security finding rides the ordinary gate path: gateError turns it into a
// dynamic security code whose exit is the family's declared exit. A family that
// PROVED a vulnerability is a verification failure (7); one that REFUSED a
// change on policy grounds is a policy denial (6).
func TestGateError_ASecurityFindingExitsByWhetherItProvedOrRefused(t *testing.T) {
	cases := []struct {
		rule string
		want aferrors.ExitCode
	}{
		{"security.authz.idor", aferrors.ExitVerification},
		{"security.injection.sql", aferrors.ExitVerification},
		{"security.canary_leak.cross_tenant", aferrors.ExitVerification},
		{"security.db_security.rls_disabled", aferrors.ExitVerification},
		{"security.headers.permissive_cors", aferrors.ExitPolicyDenied},
		{"security.supply_chain.known_vuln", aferrors.ExitPolicyDenied},
		{"security.side_effect.external_call", aferrors.ExitPolicyDenied},
		{"security.db_security.broad_grant", aferrors.ExitPolicyDenied},
	}
	for _, c := range cases {
		err := gateError(report.Finding{Rule: c.rule, Level: report.LevelFail, Title: "x"})
		var coded *aferrors.Error
		if !aferrors.As(err, &coded) {
			t.Fatalf("%s: gateError returned an uncoded error %v", c.rule, err)
		}
		if coded.Entry.ExitCode != c.want {
			t.Errorf("%s: exit = %d, want %d (code %s)", c.rule, coded.Entry.ExitCode, c.want, coded.Entry.Code)
		}
	}
}

// A non-security finding is untouched by the security case and still routes to
// the migration default. This is the negative arm: it proves the prefix check
// is what selects the security path, so breaking the prefix moves these into
// the wrong code.
func TestGateError_ANonSecurityFindingIsNotASecurityCode(t *testing.T) {
	err := gateError(report.Finding{Rule: "migration_lint", Level: report.LevelFail, Title: "x"})
	var coded *aferrors.Error
	if !aferrors.As(err, &coded) {
		t.Fatalf("gateError returned an uncoded error %v", err)
	}
	if coded.Entry.Code == aferrors.AFDSC001 || coded.Entry.Code == aferrors.AFDSC002 {
		t.Errorf("a migration finding was routed to a security code %s", coded.Entry.Code)
	}
}
