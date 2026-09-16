package env

import (
	"reflect"
	"testing"
)

const mixedExplorations = `{"explorations":[{"name":"before","outcome":{"verdict":"pass"}},{"name":"broken","visited":17},{"name":"after","outcome":{"verdict":"pass"}}]}`

func TestUnreadableExplorationDoesNotDiscardItsNeighbours(t *testing.T) {
	r, err := decodeExplorationReport([]byte(mixedExplorations))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, x := range r.Explorations {
		names = append(names, x.Name)
	}
	if !reflect.DeepEqual(names, []string{"before", "broken", "after"}) {
		t.Fatalf("lost results: %v", names)
	}
}

func TestUnreadableExplorationIsBlocked(t *testing.T) {
	r, err := decodeExplorationReport([]byte(mixedExplorations))
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Explorations[1].Outcome.Verdict; got != "blocked" {
		t.Fatalf("unreadable result is %q", got)
	}
}

func TestNullExplorationIsAnExplicitBlockedResult(t *testing.T) {
	r, err := decodeExplorationReport([]byte(`{"explorations":[null]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Explorations[0].Outcome.Verdict; got != "blocked" {
		t.Fatalf("null result is %q", got)
	}
}

func TestNonJSONExplorationReportRefuses(t *testing.T) {
	if _, err := decodeExplorationReport([]byte("unreadable")); err == nil {
		t.Fatal("unreadable document was accepted")
	}
}

func TestUnknownExplorationVerdictIsBlocked(t *testing.T) {
	r, err := decodeExplorationReport([]byte(`{"explorations":[{"name":"goal","outcome":{"verdict":"future"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Explorations[0].Outcome.Verdict; got != "blocked" {
		t.Fatalf("unknown result is %q", got)
	}
}

func TestANamelessExplorationIsBlocked(t *testing.T) {
	r, err := decodeExplorationReport([]byte(`{"explorations":[{"outcome":{"verdict":"pass"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Explorations[0].Outcome.Verdict; got != "blocked" {
		t.Fatalf("a result the runner did not name is %q", got)
	}
}

// The runner emits structured per-persona observations on the same JSON as the
// rest of an exploration, and they must survive the boundary field for field so
// the authz differential reads what the browser observed. This decodes a runner
// document carrying one cross-user reach and asserts every bounded field lands,
// which is the shape contract the collector then folds for the authz family.
func TestExplorationObservationsCrossTheRunnerBoundary(t *testing.T) {
	const doc = `{"explorations":[{"name":"reach an order","outcome":{"verdict":"pass"},"observations":[{"route":"/api/orders/{id}","method":"GET","actorTenant":"org_a","actorUser":"alice","actorRole":"member","objectClass":"another user's order","ownerTenant":"org_a","ownerUser":"bob","ownerRole":"member","status":200,"victimContentPresent":true,"setupConfirmed":true}]}]}`
	r, err := decodeExplorationReport([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Explorations) != 1 {
		t.Fatalf("want one exploration, got %d", len(r.Explorations))
	}
	obs := r.Explorations[0].Observations
	if len(obs) != 1 {
		t.Fatalf("want one observation across the boundary, got %d", len(obs))
	}
	o := obs[0]
	if o.Route != "/api/orders/{id}" || o.Method != "GET" {
		t.Fatalf("location did not survive: %q %q", o.Route, o.Method)
	}
	if o.ActorUser != "alice" || o.OwnerUser != "bob" || o.ActorTenant != "org_a" || o.OwnerTenant != "org_a" {
		t.Fatalf("identities did not survive: actor=%s owner=%s", o.ActorUser, o.OwnerUser)
	}
	if o.Status != 200 || !o.VictimContentPresent || !o.SetupConfirmed {
		t.Fatalf("outcome flags did not survive: status=%d present=%v setup=%v", o.Status, o.VictimContentPresent, o.SetupConfirmed)
	}
}
