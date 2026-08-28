package main

import (
	"reflect"
	"strings"
	"testing"
)

// The groups that were in this subscription before Antifailure was. Every one
// of them must be refused, by name, without the test needing Azure. If somebody
// relaxes the prefix rule, this is what fails.
func TestForeignGroupsAreRefused(t *testing.T) {
	for _, g := range []string{
		"Ravioli", "bonfire", "NetworkWatcherRG",
		"ME_bonfire-aca-env_bonfire_eastus", "postiz-rg",
		// Case is not a way round it.
		"ravioli", "POSTIZ-RG",
	} {
		if err := nameIsOurs(g); err == nil {
			t.Errorf("nameIsOurs(%q) allowed it; ISOLATION.md says this project never touches it", g)
		}
	}
}

func TestOurGroupsAreAllowed(t *testing.T) {
	for _, g := range []string{"af-cp-scus", "af-dev-scus", "af-corpus-scus", "af-tfstate-scus"} {
		if err := nameIsOurs(g); err != nil {
			t.Errorf("nameIsOurs(%q) refused a group this project owns: %v", g, err)
		}
	}
}

// A name that merely contains af- is not a name that starts with it. This is
// the shape of near-miss that a substring check would wave through.
func TestNearMissesAreRefused(t *testing.T) {
	for _, g := range []string{"", "  ", "prod-af-cp", "AF-CP-SCUS", "afcp", "xaf-cp"} {
		if err := nameIsOurs(g); err == nil {
			t.Errorf("nameIsOurs(%q) allowed it", g)
		}
	}
}

func TestGroupsIn(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"az short flag", []string{"az", "group", "delete", "-g", "af-cp-scus"}, []string{"af-cp-scus"}},
		{"az long flag", []string{"az", "storage", "account", "create", "--resource-group", "af-dev-scus"}, []string{"af-dev-scus"}},
		{"az long flag with equals", []string{"az", "x", "--resource-group=af-cp-scus"}, []string{"af-cp-scus"}},
		{"terraform var", []string{"terraform", "apply", "-var", "resource_group_name=af-cp-scus"}, []string{"af-cp-scus"}},
		{"terraform var with equals", []string{"terraform", "apply", "-var=resource_group_name=af-cp-scus"}, []string{"af-cp-scus"}},
		{"quoted value", []string{"terraform", "apply", "-var", `resource_group_name="af-cp-scus"`}, []string{"af-cp-scus"}},
		{"an unrelated var is not a group", []string{"terraform", "apply", "-var", "location=southcentralus"}, nil},
		{"several", []string{"az", "x", "-g", "af-a", "--resource-group", "af-b"}, []string{"af-a", "af-b"}},
		{"none", []string{"terraform", "plan"}, nil},
		{"flag at the very end with no value", []string{"az", "group", "delete", "-g"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := groupsIn(c.args); !reflect.DeepEqual(got, c.want) {
				t.Errorf("groupsIn(%v) = %v, want %v", c.args, got, c.want)
			}
		})
	}
}

// The guard fails closed. A command it cannot read is refused rather than run,
// because the alternative is guessing about `az group delete`.
func TestGuardRefusesACommandNamingNoGroup(t *testing.T) {
	if code := cmdGuard([]string{"--", "az", "group", "delete"}); code != 1 {
		t.Errorf("cmdGuard on a command naming no group returned %d, want 1", code)
	}
}

func TestGuardRunsAnAllowedCommand(t *testing.T) {
	if code := cmdGuard([]string{"--", "true", "-g", "af-cp-scus"}); code != 0 {
		t.Errorf("cmdGuard refused a command against our own group: %d", code)
	}
}

func TestGuardRefusesAForeignGroup(t *testing.T) {
	if code := cmdGuard([]string{"--", "false", "-g", "Ravioli"}); code != 1 {
		t.Errorf("cmdGuard allowed an operation on Ravioli")
	}
}

// The refusal has to name the project whose resources it protected. A generic
// message is one somebody works around instead of reading.
func TestRefusalNamesTheOwner(t *testing.T) {
	err := nameIsOurs("postiz-rg")
	if err == nil || !strings.Contains(err.Error(), "postiz") {
		t.Errorf("refusal did not name the owner: %v", err)
	}
}

// The group Azure makes that this project did not ask for.
//
// Creating a Container Apps environment produces ME_<env>_<group>_<location>
// alongside it. It is refused, because operating on it directly breaks a
// running environment, and the REASON matters as much as the refusal: the
// generic message would tell a reader it belongs to somebody else's project,
// which is a confident wrong answer about a group this project's own apply
// caused to exist.
func TestTheGroupAzureMakesForAContainerAppsEnvironmentIsRefusedForTheRightReason(t *testing.T) {
	const g = "ME_afcp-env_af-cp-centralus_centralus"
	err := nameIsOurs(g)
	if err == nil {
		t.Fatalf("%s does not start with af- and was allowed", g)
	}
	for _, want := range []string{"Azure", "Container Apps", "Delete the environment", "project=antifailure"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal for %s should mention %q, got: %v", g, want, err)
		}
	}
	// And it must NOT claim the group belongs to another project. Matching on
	// "belongs to" alone is too crude, because the correct message legitimately
	// says the group's lifecycle belongs to its environment; the phrase that
	// would be wrong is the foreign-group one.
	if strings.Contains(err.Error(), "not to Antifailure") {
		t.Errorf("refusal for %s reads as somebody else's group, which is wrong: %v", g, err)
	}
}

// bonfire's own environment group keeps its named refusal, because that one
// really does belong to somebody else and the ME_ rule must not swallow it.
func TestBonfiresEnvironmentGroupIsStillNamedAsForeign(t *testing.T) {
	err := nameIsOurs("ME_bonfire-aca-env_bonfire_eastus")
	if err == nil {
		t.Fatal("bonfire's environment group was allowed")
	}
	if !strings.Contains(err.Error(), "bonfire") {
		t.Errorf("refusal should name bonfire so somebody knows whose work it protected: %v", err)
	}
}
