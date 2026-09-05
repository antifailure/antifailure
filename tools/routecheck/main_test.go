package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The inventory as the repository really writes it, so a change to the file's
// shape that this parser cannot read fails here rather than silently reducing
// the number of routes checked.
const inventoryFixture = `import { CONTROL_PLANE_URL } from "./site";

export const CONTROL_PLANE_ROUTES = {
  "applications.create": {
    method: "POST",
    path: "/v1/applications",
    calledFrom: "components/pages/company/ApplicationForm.tsx",
    whenMissing:
      "The careers form says 'Could not reach the server'.",
    probeEffect: "inert",
    probeReason:
      "An origin guard answers 403 before the handler.",
  },
  "auth.github": {
    method: "GET",
    path: "/auth/github",
    calledFrom: "components/AuthScreen.tsx",
    whenMissing: "Continue with GitHub lands on a 404.",
    probeEffect: "writes",
    probeReason:
      "beginSignIn inserts one oauth_states row.",
  },
} as const satisfies Record<string, ControlPlaneRoute>;
`

func writeFile(t *testing.T, dir, rel, body string) string {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseInventoryReadsEveryRouteAndItsProbeCost(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "inventory.ts", inventoryFixture)
	routes, err := ParseInventory(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("read %d routes, want 2: %+v", len(routes), routes)
	}
	if routes[0].Method != "POST" || routes[0].Path != "/v1/applications" {
		t.Errorf("first route is %s %s", routes[0].Method, routes[0].Path)
	}
	if !routes[0].Inert() {
		t.Error("the applications route is declared inert and did not read as inert")
	}
	if routes[1].Inert() {
		t.Error("the auth route is declared as writing and read as inert, which would send a probe nobody authorised")
	}
	if !strings.Contains(routes[0].WhenMissingLine(), "careers form") {
		t.Errorf("whenMissing did not survive the parse: %q", routes[0].WhenMissingLine())
	}
}

// The parser must FAIL on an inventory it cannot read, never return the part it
// understood. A short read is a route nothing checks.
func TestParseInventoryRefusesAnEntryItCannotRead(t *testing.T) {
	cases := map[string]string{
		"no method":       strings.Replace(inventoryFixture, `    method: "POST",`+"\n", "", 1),
		"unknown effect":  strings.Replace(inventoryFixture, `"inert"`, `"probably fine"`, 1),
		"no probe reason": strings.Replace(inventoryFixture, "    probeReason:\n      \"An origin guard answers 403 before the handler.\",\n", "", 1),
		"renamed export":  strings.Replace(inventoryFixture, "export const CONTROL_PLANE_ROUTES", "export const ROUTES_V2", 1),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeFile(t, dir, "inventory.ts", body)
			if _, err := ParseInventory(path); err == nil {
				t.Fatal("parsed an inventory it should have refused")
			}
		})
	}
}

func TestFindStrayCallSitesNamesTheFileAndLine(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "lib/site.ts", "export const CONTROL_PLANE_URL = \"x\";\n")
	writeFile(t, dir, "lib/control-plane-routes.ts", "import { CONTROL_PLANE_URL } from \"./site\";\n")
	writeFile(t, dir, "components/Ok.tsx", "import { controlPlaneUrl } from \"@/lib/control-plane-routes\";\n")
	writeFile(t, dir, "components/Bad.tsx", "const a = 1;\nfetch(`${CONTROL_PLANE_URL}/v1/sneaky`);\n")

	strays, err := FindStrayCallSites(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(strays) != 1 {
		t.Fatalf("found %d strays, want 1: %+v", len(strays), strays)
	}
	if !strings.HasSuffix(strays[0].File, filepath.Join("components", "Bad.tsx")) {
		t.Errorf("named %q", strays[0].File)
	}
	if strays[0].Line != 2 {
		t.Errorf("reported line %d, want 2", strays[0].Line)
	}
}

func TestFindStrayCallSitesIgnoresProseAndVendoredCode(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "lib/site.ts", "export const CONTROL_PLANE_URL = \"x\";\n")
	writeFile(t, dir, "lib/control-plane-routes.ts", "// inventory\n")
	writeFile(t, dir, "lib/beacon.ts", "// follows CONTROL_PLANE_URL for the same reason\n/* also CONTROL_PLANE_URL\n   here */\nexport const E = 1;\n")
	writeFile(t, dir, "node_modules/pkg/index.js", "const x = CONTROL_PLANE_URL;\n")

	strays, err := FindStrayCallSites(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(strays) != 0 {
		t.Fatalf("tripped on prose or on node_modules: %+v", strays)
	}
}

func TestParseBoundaryRegisterReadsTheKeys(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "boundary.ts", `export const ROUTES = {
  'POST /v1/leads': {
    audience: 'excluded',
  },
  'GET /auth/github': {
    audience: 'excluded',
  },
}`)
	served, err := ParseBoundaryRegister(path)
	if err != nil {
		t.Fatal(err)
	}
	if !served["POST /v1/leads"] || !served["GET /auth/github"] {
		t.Fatalf("read %v", served)
	}
	if served["POST /v1/applications"] {
		t.Error("invented a route the register does not hold")
	}
}

func TestParseBoundaryRegisterRefusesAnEmptyRead(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "boundary.ts", "export const ROUTES = {}\n")
	if _, err := ParseBoundaryRegister(path); err == nil {
		t.Fatal("an empty register passed, which would let every route through unchecked")
	}
}

// THE HOLE THIS SCANNER SHIPPED WITH, and the reason it is string aware.
//
// stripComments used to break out of the line at the first `//` it saw,
// wherever it saw it. So this call site:
//
//	const u = "https://antifailure.dev" + CONTROL_PLANE_URL;
//
// had everything after `https:` discarded as a comment, the identifier was
// never seen, and the gate reported a clean run over a control plane URL built
// outside the inventory. That is a false NEGATIVE, which is silent, in the one
// instrument whose whole job is to make an unenumerable call site loud.
func TestStripCommentsDoesNotLoseCodeAfterAUrlInAString(t *testing.T) {
	kept := []struct{ name, line string }{
		{"a url in a double quoted string", `const u = "https://antifailure.dev" + CONTROL_PLANE_URL;`},
		{"a url in a single quoted string", `const u = 'https://antifailure.dev' + CONTROL_PLANE_URL;`},
		{"a url in a template literal", "const u = `https://antifailure.dev${CONTROL_PLANE_URL}`;"},
		{"the identifier inside a template expression", "fetch(`${CONTROL_PLANE_URL}/v1/sneaky`);"},
		{"an escaped quote before it", `const u = "he said \"hi\" //" + CONTROL_PLANE_URL;`},
		{"code after a block comment closes", `/* a url https:// in prose */ const u = CONTROL_PLANE_URL;`},
	}
	for _, c := range kept {
		t.Run(c.name, func(t *testing.T) {
			code, _ := stripComments(c.line, scanState{})
			if !strings.Contains(code, "CONTROL_PLANE_URL") {
				t.Errorf("the identifier was lost, so a call site written this way passes unseen.\n  line:    %s\n  stripped: %s", c.line, code)
			}
		})
	}

	dropped := []struct{ name, line string }{
		{"a line comment", `// follows CONTROL_PLANE_URL for the same reason`},
		{"a trailing line comment", `const a = 1; // CONTROL_PLANE_URL is not used here`},
		{"a single line block comment", `/* CONTROL_PLANE_URL in prose */`},
	}
	for _, c := range dropped {
		t.Run(c.name, func(t *testing.T) {
			code, _ := stripComments(c.line, scanState{})
			if strings.Contains(code, "CONTROL_PLANE_URL") {
				t.Errorf("prose was read as code, which is a rule people route around by rewording a sentence.\n  line:     %s\n  stripped: %s", c.line, code)
			}
		})
	}
}

// A template literal survives a newline, so the state has to as well, or the
// line after a multi line template is scanned in the wrong mode.
func TestStripCommentsCarriesATemplateLiteralAcrossLines(t *testing.T) {
	_, st := stripComments("const q = `line one // not a comment", scanState{})
	if !st.inTemplate {
		t.Fatal("an open template literal did not carry to the next line")
	}
	code, st := stripComments("line two` + CONTROL_PLANE_URL;", st)
	if st.inTemplate {
		t.Error("the closing backtick did not end the template")
	}
	if !strings.Contains(code, "CONTROL_PLANE_URL") {
		t.Errorf("lost the identifier after a multi line template: %s", code)
	}

	_, st = stripComments("/* a block comment opens", scanState{})
	if !st.inComment {
		t.Fatal("an open block comment did not carry to the next line")
	}
	code, _ = stripComments("CONTROL_PLANE_URL still inside it */", st)
	if strings.Contains(code, "CONTROL_PLANE_URL") {
		t.Errorf("read the inside of a block comment as code: %s", code)
	}
}

// The suite is skipped, and that is a decision rather than an oversight. The
// test pinning these URLs has to import CONTROL_PLANE_URL to assert that
// controlPlaneUrl() still produces the string the call sites used to build by
// hand, and a rule refusing that would forbid the one check proving the
// refactor changed nothing. Nothing under www/test reaches a browser.
//
// The exclusion has to be the suite DIRECTORY and not the word "test", or a
// component named for what it renders would walk through it.
func TestFindStrayCallSitesSkipsTheSuiteButNotAComponent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "lib/site.ts", "export const CONTROL_PLANE_URL = \"x\";\n")
	writeFile(t, dir, "lib/control-plane-routes.ts", "// inventory\n")
	writeFile(t, dir, "test/control-plane-routes.test.ts", "import { CONTROL_PLANE_URL } from '../lib/site'\n")

	strays, err := FindStrayCallSites(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(strays) != 0 {
		t.Fatalf("the suite was treated as a call site: %+v", strays)
	}

	writeFile(t, dir, "components/TestimonialCard.tsx", "fetch(`${CONTROL_PLANE_URL}/v1/sneaky`);\n")
	strays, err = FindStrayCallSites(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(strays) != 1 {
		t.Fatalf("found %d strays, want the one in components/: %+v", len(strays), strays)
	}
}

// THE HOLE, END TO END. The unit test above proves the scanner keeps the
// identifier; this proves the COMMAND refuses the file. They are separate
// because stripComments returning the right string is worth nothing if the
// walk never asks it about that file.
func TestFindStrayCallSitesSeesACallSiteHiddenBehindAUrl(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "lib/site.ts", "export const CONTROL_PLANE_URL = \"x\";\n")
	writeFile(t, dir, "lib/control-plane-routes.ts", "// inventory\n")
	// Line 1 is the failure exactly as it was found: the URL and the identifier
	// on the SAME line, which is the only arrangement the old stripper could
	// swallow. An earlier version of this test put them on separate lines and
	// stayed GREEN with the hole reopened, which the mutation run caught. Line 2
	// is the split arrangement, kept so the easy case cannot silently regress.
	writeFile(t, dir, "components/Sneaky.tsx",
		"const u = \"https://antifailure.dev\" + CONTROL_PLANE_URL + \"/v1/sneaky\";\n"+
			"const v = base + CONTROL_PLANE_URL;\n")

	strays, err := FindStrayCallSites(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(strays) != 2 {
		t.Fatalf("found %d strays, want 2. A call site whose own line holds a URL was invisible: %+v", len(strays), strays)
	}
	if strays[0].Line != 1 {
		t.Errorf("reported line %d for the same-line call site, want 1", strays[0].Line)
	}
	if strays[1].Line != 2 {
		t.Errorf("reported line %d for the split call site, want 2", strays[1].Line)
	}
}
