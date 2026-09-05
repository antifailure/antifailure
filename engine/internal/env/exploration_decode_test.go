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
