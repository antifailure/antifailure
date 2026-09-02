package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

// No trailing slash, on either spelling. The host sets "trailingSlash":
// "never", so the slashed form is a 301, and this function writes the "More"
// link under all 131 error codes.
//
// The fragment cases stay, because they are the older defect and it is still
// possible to reintroduce it: treating the whole field as a path built
// /docs/reference/cli#af-init/, whose fragment is "af-init/" and matches no
// heading. The link resolved, landed at the top of a 900 line page, and looked
// like it worked. lychee found that one against the built site.
func TestDocsURLWritesTheAddressTheHostServes(t *testing.T) {
	cases := map[string]string{
		"reference/cli":              "/docs/reference/cli",
		"reference/cli#af-init":      "/docs/reference/cli#af-init",
		"concepts/egress#inspection": "/docs/concepts/egress#inspection",
	}
	for in, want := range cases {
		if got := docsURL(in); got != want {
			t.Errorf("docsURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPublicCatalogCarriesTheFieldsAnAgentNeedsToRecover(t *testing.T) {
	raw, err := renderJSON([]entry{{
		Code: "AF-DB-002", Area: "DB", Message: "The database did not answer.",
		NextStep: "Check the connection and retry.", Docs: "reference/errors#af-db-002",
		Retryable: true, ExitCode: 5,
	}})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		SchemaVersion int `json:"schemaVersion"`
		Errors        []struct {
			Code, AreaName, Message, Resolution, Docs string
			Retryable                                 bool
			ExitCode                                  int
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("generated catalog is not JSON: %v", err)
	}
	if got.SchemaVersion != 1 || len(got.Errors) != 1 {
		t.Fatalf("unexpected envelope: %+v", got)
	}
	e := got.Errors[0]
	if e.Code != "AF-DB-002" || e.AreaName != "Database" || !e.Retryable || e.ExitCode != 5 {
		t.Fatalf("identity and behavior were lost: %+v", e)
	}
	if e.Message == "" || e.Resolution == "" {
		t.Fatalf("an agent cannot explain and recover from this entry: %+v", e)
	}
	if e.Docs != "https://antifailure.dev/docs/reference/errors#af-db-002" {
		t.Fatalf("docs = %q", e.Docs)
	}
}

func TestPublicCatalogLeavesOutErrorsThisVersionCannotReturn(t *testing.T) {
	planned := "AF-DB-" + "001"
	raw, err := renderJSON([]entry{
		{Code: planned, Area: "DB", Message: "Planned.", NextStep: "Wait.", Docs: "reference/errors", Planned: true},
		{Code: "AF-DB-002", Area: "DB", Message: "Reachable.", NextStep: "Retry.", Docs: "reference/errors"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) == "" || !bytes.Contains(raw, []byte("AF-DB-002")) {
		t.Fatal("the reachable error was not published")
	}
	if bytes.Contains(raw, []byte(planned)) {
		t.Fatal("a planned error was published as though this version could return it")
	}
}
