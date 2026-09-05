package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A tree with the two files the offline half reads, so each test can state the
// exact disagreement it is about rather than depending on the real repository,
// which is fixed and would make every one of these permanently green.
func tree(t *testing.T, hosts, production, staging string) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(hostnamesPath, hosts)
	write(tfvarsPaths[0], production)
	write(tfvarsPaths[1], staging)
	return root
}

const bothOrigins = "site_origin = \"https://antifailure.dev,https://www.antifailure.dev\"\n"
const apexOnly = "site_origin = \"https://antifailure.dev\"\n"

func TestOriginsRefusesAHostnameWithNoAllowedOrigin(t *testing.T) {
	// THE FAILURE, exactly as it was: two hostnames served, one origin
	// configured, and every check green because they all asked the apex.
	root := tree(t, "antifailure.dev\nwww.antifailure.dev\n", apexOnly, bothOrigins)
	if code := cmdOrigins(root); code != 1 {
		t.Fatalf("a hostname with no allowed origin has to be refused, got exit %d", code)
	}
}

func TestOriginsPassesWhenEveryHostnameIsAllowed(t *testing.T) {
	root := tree(t, "antifailure.dev\nwww.antifailure.dev\n", bothOrigins, bothOrigins)
	if code := cmdOrigins(root); code != 0 {
		t.Fatalf("the fixed tree has to pass, got exit %d", code)
	}
}

func TestOriginsRefusesAnOriginTheSiteIsNotServedOn(t *testing.T) {
	// The other direction. An allowed origin with no hostname behind it is a
	// widened boundary on the unauthenticated write routes, and it reads
	// exactly like a leftover, which is why nobody removes it.
	root := tree(t, "antifailure.dev\n", bothOrigins, apexOnly)
	if code := cmdOrigins(root); code != 1 {
		t.Fatalf("an origin with no hostname behind it has to be refused, got exit %d", code)
	}
}

func TestOriginsChecksEveryTfvarsAndNotJustTheFirst(t *testing.T) {
	// Production right and staging stale still has to fail: staging is where
	// the two origin path is exercised before production runs it, so a staging
	// file left behind means the first exercise is on the plane a visitor is
	// standing on.
	root := tree(t, "antifailure.dev\nwww.antifailure.dev\n", bothOrigins, apexOnly)
	if code := cmdOrigins(root); code != 1 {
		t.Fatalf("a stale staging file has to be refused, got exit %d", code)
	}
}

func TestOriginsCannotTellWhenTheVariableIsAbsent(t *testing.T) {
	// An absent assignment is NOT an empty one, and the difference decides
	// whether a control plane refuses every form on purpose. Reporting absent
	// as "no origins, and none were needed" would make the gate pass over the
	// one file it most needs to read.
	root := tree(t, "antifailure.dev\n", "operator_portal_enabled = true\n", bothOrigins)
	if code := cmdOrigins(root); code != 2 {
		t.Fatalf("an absent site_origin has to be a could-not-tell, got exit %d", code)
	}
}

func TestOriginsCannotTellWhenThereAreNoHostnames(t *testing.T) {
	// An empty hostname list makes every comparison vacuously true. A gate that
	// passes because it had nothing to compare is the exact shape of a check
	// that cannot say no.
	root := tree(t, "# nothing but a comment\n", bothOrigins, bothOrigins)
	if code := cmdOrigins(root); code != 2 {
		t.Fatalf("an empty hostname list has to be a could-not-tell, got exit %d", code)
	}
}

func TestHostnamesRefusesAnOriginWhereAHostnameBelongs(t *testing.T) {
	// Two notations for the same thing is how the file and the tfvars drift
	// into never matching while both look right.
	root := tree(t, "https://antifailure.dev\n", bothOrigins, bothOrigins)
	if _, err := hostnames(root); err == nil {
		t.Fatal("a scheme in the hostname file has to be refused")
	}
}

func TestSiteOriginSplitsTheValueTheProcessWillSplit(t *testing.T) {
	// The comment line is there on purpose: a reader that matched the word
	// anywhere would take a sentence about the variable for an assignment of it,
	// and this file is full of sentences about the variable.
	root := tree(t, "antifailure.dev\n",
		"# a comment mentioning site_origin that is not an assignment\n"+bothOrigins,
		"site_origin = \" https://a.test , https://b.test \" # trailing comment\n")
	got, err := siteOrigins(root, tfvarsPaths[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "https://antifailure.dev" || got[1] != "https://www.antifailure.dev" {
		t.Fatalf("the value read as %v", got)
	}
	// Whitespace around a separator is somebody formatting, not an origin, and
	// the application trims it the same way.
	got, err = siteOrigins(root, tfvarsPaths[1])
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "https://a.test" || got[1] != "https://b.test" {
		t.Fatalf("a spaced value read as %v", got)
	}
}

func TestSiteOriginRefusesAStrayComma(t *testing.T) {
	// The control plane stops the process on one, because ",,," would otherwise
	// read as a configured list. A gate that quietly dropped it would pass a
	// tfvars that will not boot.
	root := tree(t, "antifailure.dev\n", "site_origin = \"https://antifailure.dev,\"\n", bothOrigins)
	if _, err := siteOrigins(root, tfvarsPaths[0]); err == nil {
		t.Fatal("a stray comma has to be refused")
	}
}

func TestExemptionsRequireAReason(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, exemptionsPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("some.host.test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// An exemption with no argument behind it cannot be told apart from
	// somebody silencing a finding they did not understand.
	if _, _, err := exemptions(root); err == nil {
		t.Fatal("an exemption with no reason has to be refused")
	}
}

// A control plane that answers only the origins it is given, so `live` can be
// pointed at both states without a network.
func plane(allowed ...string) *httptest.Server {
	ok := map[string]bool{}
	for _, o := range allowed {
		ok[o] = true
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("origin")
		w.Header().Set("vary", "origin")
		if !ok[origin] {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("access-control-allow-origin", origin)
		w.WriteHeader(http.StatusNoContent)
	}))
}

func TestLiveRefusesAPlaneThatWillNotAnswerAHostname(t *testing.T) {
	root := tree(t, "antifailure.dev\nwww.antifailure.dev\n", bothOrigins, bothOrigins)
	s := plane("https://antifailure.dev")
	defer s.Close()
	if code := cmdLive(root, s.URL); code != 1 {
		t.Fatalf("a plane that refuses www has to be refused, got exit %d", code)
	}
}

func TestLivePassesAPlaneThatAnswersEveryHostname(t *testing.T) {
	root := tree(t, "antifailure.dev\nwww.antifailure.dev\n", bothOrigins, bothOrigins)
	s := plane("https://antifailure.dev", "https://www.antifailure.dev")
	defer s.Close()
	if code := cmdLive(root, s.URL); code != 0 {
		t.Fatalf("a plane that answers both has to pass, got exit %d", code)
	}
}

func TestLiveRefusesAHeaderCarryingSomebodyElsesOrigin(t *testing.T) {
	// A response that carries an allow header for the WRONG origin is refused
	// by the browser and passes any check that only looks for the header's
	// presence. That is the failure mode a list introduces and a single value
	// did not have.
	root := tree(t, "antifailure.dev\nwww.antifailure.dev\n", bothOrigins, bothOrigins)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("access-control-allow-origin", "https://antifailure.dev")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer s.Close()
	if code := cmdLive(root, s.URL); code != 1 {
		t.Fatalf("echoing the wrong allowed origin has to be refused, got exit %d", code)
	}
}

func TestLiveReportsAnAbsentRouteAsNotCheckedRatherThanAsAPass(t *testing.T) {
	// Merging to main deploys staging only, so a front end can ship a route the
	// tagged production image does not serve. That is a real defect and a
	// DIFFERENT one: the origin was never compared, so calling it a pass would
	// be claiming an answer to a question nobody asked.
	root := tree(t, "antifailure.dev\nwww.antifailure.dev\n", bothOrigins, bothOrigins)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer s.Close()
	if code := cmdLive(root, s.URL); code != 2 {
		t.Fatalf("an absent route has to be a could-not-tell, got exit %d", code)
	}
}

func TestLiveCannotTellWhenTheControlPlaneDoesNotAnswer(t *testing.T) {
	root := tree(t, "antifailure.dev\n", bothOrigins, bothOrigins)
	s := plane()
	url := s.URL
	s.Close()
	if code := cmdLive(root, url); code != 2 {
		t.Fatalf("an unreachable control plane has to be a could-not-tell, got exit %d", code)
	}
}

func TestEveryCrossOriginRouteCarriesWhatAVisitorSees(t *testing.T) {
	// The list is what `live` prints as "a route not listed above was NOT
	// checked", so a row with no sentence is a row nobody can act on.
	if len(crossOriginRoutes) == 0 {
		t.Fatal("no cross origin routes declared, so live would check nothing and pass")
	}
	for _, r := range crossOriginRoutes {
		if !strings.HasPrefix(r.path, "/") || strings.TrimSpace(r.visible) == "" {
			t.Fatalf("%q has no path or no visitor-facing consequence", r.path)
		}
	}
}

func TestWorstPrefersARefusalOverACouldNotTell(t *testing.T) {
	// `all` reports one code. A known broken hostname is worse to be quiet
	// about than an unasked question, and both are worse than success.
	if got := worst(0, 2, 1); got != 1 {
		t.Fatalf("a refusal has to win, got %d", got)
	}
	if got := worst(0, 2, 0); got != 2 {
		t.Fatalf("a could-not-tell has to outrank success, got %d", got)
	}
	if got := worst(0, 0, 0); got != 0 {
		t.Fatalf("all clear has to be clear, got %d", got)
	}
}

// ---------------------------------------------------------------------------
// What Azure says, against what the tree claims
// ---------------------------------------------------------------------------

func domain(name string) customDomain { return customDomain{DomainName: name, Status: "Ready"} }

func TestDomainsRefusesAHostnameBoundInAzureAndAbsentFromTheTree(t *testing.T) {
	// THE ONE THIS HALF EXISTS FOR. Somebody binds a hostname in the portal,
	// nothing in this repository learns about it, no origin is configured for
	// it, and every call a page on it makes is refused. That is the same defect
	// as the www outage, arriving from the direction no file can see.
	code := compareDomains(
		[]string{"antifailure.dev"}, map[string]string{}, nil,
		[]customDomain{domain("antifailure.dev"), domain("www.antifailure.dev")},
	)
	if code != 1 {
		t.Fatalf("a bound hostname the tree does not name has to be refused, got exit %d", code)
	}
}

func TestDomainsPassesWhenTheTreeNamesExactlyWhatIsBound(t *testing.T) {
	code := compareDomains(
		[]string{"antifailure.dev", "www.antifailure.dev"}, map[string]string{}, nil,
		[]customDomain{domain("antifailure.dev"), domain("www.antifailure.dev")},
	)
	if code != 0 {
		t.Fatalf("an exact match has to pass, got exit %d", code)
	}
}

func TestDomainsAllowsAnExemptedHostnameAndOnlyWithAReason(t *testing.T) {
	// The platform's own default hostname really does serve the site and really
	// must not be an allowed origin. Exempting it is a decision with an
	// argument behind it, which is why it is a row rather than a rule.
	code := compareDomains(
		[]string{"antifailure.dev"},
		map[string]string{"default.azurestaticapps.net": "never published and not in any DNS record this project owns"},
		[]string{"default.azurestaticapps.net"},
		[]customDomain{domain("antifailure.dev"), domain("default.azurestaticapps.net")},
	)
	if code != 0 {
		t.Fatalf("an exempted hostname has to be allowed, got exit %d", code)
	}
}

func TestDomainsRefusesAStaleExemption(t *testing.T) {
	// A row that has stopped being needed. Without this the file rots into a
	// permanent allowance, which is how a hand maintained list stops meaning
	// anything.
	code := compareDomains(
		[]string{"antifailure.dev"},
		map[string]string{"gone.azurestaticapps.net": "it was the default hostname"},
		[]string{"gone.azurestaticapps.net"},
		[]customDomain{domain("antifailure.dev")},
	)
	if code != 1 {
		t.Fatalf("a stale exemption has to be refused, got exit %d", code)
	}
}

func TestDomainsRefusesAHostnameTheTreeClaimsAndAzureDoesNotServe(t *testing.T) {
	// Its origin sits in the CORS allowlist for a page that does not exist, and
	// check-tls.sh asks it for a certificate forever.
	code := compareDomains(
		[]string{"antifailure.dev", "retired.antifailure.dev"}, map[string]string{}, nil,
		[]customDomain{domain("antifailure.dev")},
	)
	if code != 1 {
		t.Fatalf("a claimed hostname Azure does not serve has to be refused, got exit %d", code)
	}
}
