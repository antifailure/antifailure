package main

import "testing"

func TestARecursiveSubtreeDoesNotBecomeTheWholeModule(t *testing.T) {
	gates := gatesIn("go test ./internal/secrets/...", "engine")
	if len(gates) != 1 || gates[0].arg != "./internal/secrets/..." {
		t.Fatalf("a subtree was widened: %#v", gates)
	}
}

func TestTheEngineRunnerRetainsItsWholeModuleFootprint(t *testing.T) {
	gates := gatesIn("go run ./tools/enginetest .", rootDir)
	if len(gates) != 2 || gates[0].arg != "enginetest" || gates[1] != (gate{kind: "gotest", arg: "./...", dir: "engine"}) {
		t.Fatalf("the engine runner's complete inventory was lost: %#v", gates)
	}
}
