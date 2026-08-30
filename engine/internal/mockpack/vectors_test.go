package mockpack_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/antifailure/antifailure/engine/internal/mockpack"
)

// The control plane's billing integration is tested against this pack, and the
// control plane is TypeScript. Running the Go sidecar from a Node test would
// mean a Go toolchain in the web suite; reimplementing the pack runtime in
// TypeScript means two runtimes that will drift, and the one that drifts is the
// one nobody runs against real traffic: the control plane's tests would pass
// against responses the sidecar never produces, which is the exact failure this
// package's doc comment warns about, one level up.
//
// So the Go implementation emits its answers and the TypeScript one proves it
// reproduces them, byte for byte, the same arrangement 8.6 uses for the policy
// engine. This file is the emitter and
// web/apps/api/test/mockpack.test.ts is the consumer.
//
// The corpus is a TRANSCRIPT rather than a set of independent cases, because
// the pack is stateful: the response to GET /v1/subscriptions/{id} depends on
// what was created before it, and identifiers are minted in order. Replaying it
// out of order would produce different answers on both sides.

var updateVectors = flag.Bool("update-vectors", false,
	"rewrite schemas/mockpack-vectors.json from the current implementation")

type vectorFile struct {
	// Note is addressed to whoever opens the file wondering what it is for.
	Note string `json:"note"`
	Host string `json:"host"`
	// Steps run in order against one engine. Replaying them in any other order
	// produces different identifiers and different answers.
	Steps []vectorStep `json:"steps"`
}

type vectorStep struct {
	// Why names what this step is here to pin down, so a failure says which
	// property broke rather than which byte differs.
	Why     string `json:"why"`
	Method  string `json:"method"`
	Path    string `json:"path"`
	Body    string `json:"body,omitempty"`
	Matched bool   `json:"matched"`
	Status  int    `json:"status,omitempty"`
	// Response is the parsed body rather than the raw bytes. Whitespace in a
	// pack file is not a property either implementation should have to
	// reproduce, and comparing parsed values is what an application does.
	Response json.RawMessage `json:"response,omitempty"`
}

// steps is the transcript. Every one of the defects a real integration found in
// this pack has a step here, so a regression in either implementation is caught
// in both languages.
var steps = []struct{ why, method, path, body string }{
	{"a create answers with the provider's shape and keeps the object",
		"POST", "/v1/customers", "email=buyer%40example.test&name=A+Buyer&metadata[org_id]=org-1"},
	{"a read returns the object that was created, not a fresh one",
		"GET", "/v1/customers/cus_mock00000000000001", ""},
	{"an update merges into the stored object rather than replacing it",
		"POST", "/v1/customers/cus_mock00000000000001", "name=Renamed"},
	{"a read of something nobody created is the provider's error shape, not a bare 404",
		"GET", "/v1/customers/cus_neverexisted", ""},
	{"a checkout session's url names that session, so the read back after checkout finds it",
		"POST", "/v1/checkout/sessions",
		"mode=subscription&customer=cus_mock00000000000001&client_reference_id=org-1" +
			"&success_url=https%3A%2F%2Fshop.test%2Fok&cancel_url=https%3A%2F%2Fshop.test%2Fno"},
	{"the session reads back by the id its url carries",
		"GET", "/v1/checkout/sessions/cs_mock00000000000002", ""},
	{"a subscription carries the item it was created with, which is where the plan is read from",
		"POST", "/v1/subscriptions",
		"customer=cus_mock00000000000001&items[0][price]=price_team&items[0][quantity]=3"},
	{"a subscription with no quantity gets the provider's default rather than a null",
		"POST", "/v1/subscriptions", "customer=cus_mock00000000000001&items[0][price]=price_free"},
	{"a plan change is answered at all, and keeps the customer",
		"POST", "/v1/subscriptions/sub_mock00000000000003", "items[0][price]=price_enterprise"},
	{"an update that names no price leaves the items alone rather than emptying them",
		"POST", "/v1/subscriptions/sub_mock00000000000003", "cancel_at_period_end=true"},
	{"cancelling keeps the customer, the period and the items",
		"DELETE", "/v1/subscriptions/sub_mock00000000000003", ""},
	{"the cancelled subscription reads back cancelled and whole",
		"GET", "/v1/subscriptions/sub_mock00000000000003", ""},
	{"updating something nobody created does not quietly create it",
		"POST", "/v1/subscriptions/sub_neverexisted", "items[0][price]=price_team"},
	{"a list is in insertion order, so two runs agree",
		"GET", "/v1/subscriptions", ""},
	{"numeric fields are numbers, including one nobody supplied",
		"POST", "/v1/payment_intents", "currency=usd&customer=cus_mock00000000000001"},
	{"a client secret names its own intent",
		"POST", "/v1/payment_intents", "amount=2000&currency=usd&customer=cus_mock00000000000001"},
	{"a portal session carries the return url the application sent",
		"POST", "/v1/billing_portal/sessions",
		"customer=cus_mock00000000000001&return_url=https%3A%2F%2Fapp.test%2Fbilling"},
	{"an invoice is stored and listed",
		"POST", "/v1/invoices", "customer=cus_mock00000000000001&subscription=sub_mock00000000000003"},
	{"the invoice list carries the invoice",
		"GET", "/v1/invoices", ""},
	{"a request no route matches is reported as a miss rather than answered emptily",
		"POST", "/v1/nothing_like_this", ""},
}

func buildVectors(t *testing.T) vectorFile {
	t.Helper()
	packs, err := mockpack.Builtin()
	if err != nil {
		t.Fatal(err)
	}
	e := mockpack.New(packs)

	out := vectorFile{
		Note: "Generated by engine/internal/mockpack/vectors_test.go. The engine emits " +
			"these answers and every other implementation of the pack runtime must " +
			"reproduce them exactly. The steps are a transcript and depend on their " +
			"order. Regenerate with 'go test ./internal/mockpack -update-vectors'.",
		Host: "api.stripe.com",
	}
	for _, s := range steps {
		resp, matched := e.Answer(out.Host, s.method, s.path, []byte(s.body))
		step := vectorStep{
			Why: s.why, Method: s.method, Path: s.path, Body: s.body, Matched: matched,
		}
		if matched {
			step.Status = resp.Status
			// Round-tripped through a decode so the corpus records the values
			// rather than the pack file's indentation.
			var parsed any
			if err := json.Unmarshal(resp.Body, &parsed); err != nil {
				t.Fatalf("%s %s answered something that is not JSON: %s", s.method, s.path, resp.Body)
			}
			encoded, err := json.Marshal(parsed)
			if err != nil {
				t.Fatal(err)
			}
			step.Response = encoded
		}
		out.Steps = append(out.Steps, step)
	}
	return out
}

func vectorPath() string {
	// Shared with the web workspace, so it lives at the repository root rather
	// than inside either one's tree.
	return filepath.Join("..", "..", "..", "schemas", "mockpack-vectors.json")
}

func TestMockPackVectorsMatchTheCheckedInFile(t *testing.T) {
	want := buildVectors(t)
	encoded, err := json.MarshalIndent(want, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, '\n')

	path := vectorPath()
	if *updateVectors {
		if err := os.WriteFile(path, encoded, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s", path)
		return
	}

	have, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v\n\nRegenerate with: go test ./internal/mockpack -update-vectors", err)
	}
	if !bytes.Equal(bytes.TrimSpace(have), bytes.TrimSpace(encoded)) {
		t.Errorf("schemas/mockpack-vectors.json is out of date with the pack runtime.\n" +
			"The control plane proves its own runtime matches these answers, so a stale\n" +
			"file means the two are no longer being compared.\n\n" +
			"Regenerate with: go test ./internal/mockpack -update-vectors")
	}
}

// The corpus is only worth what it covers. A step that stops exercising a route
// kind lets the other implementation stop implementing it with nothing failing.
func TestVectorCorpusCoversEveryRouteKind(t *testing.T) {
	vectors := buildVectors(t)

	var missed, notFound, listed, merged bool
	for _, s := range vectors.Steps {
		switch {
		case !s.Matched:
			missed = true
		case s.Status == 404:
			notFound = true
		case s.Method == "GET" && !bytes.Contains(s.Response, []byte(`"object":"list"`)):
		}
		if bytes.Contains(s.Response, []byte(`"object":"list"`)) && s.Method == "GET" {
			listed = true
		}
		if (s.Method == "POST" || s.Method == "DELETE") && s.Status == 200 &&
			bytes.Contains(s.Response, []byte(`"object":"subscription"`)) &&
			bytes.Contains(s.Response, []byte(`"customer":"cus_`)) {
			merged = true
		}
	}
	if !missed {
		t.Error("no step misses every route, so the refusal path is not compared")
	}
	if !notFound {
		t.Error("no step reads something that does not exist, so the not_found shape is not compared")
	}
	if !listed {
		t.Error("no step lists a collection, so the list shape is not compared")
	}
	if !merged {
		t.Error("no step merges into a stored object, so the update path is not compared")
	}
}
