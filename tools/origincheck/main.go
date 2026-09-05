// Command origincheck refuses a marketing site whose own control plane will not
// answer one of the hostnames it is served on.
//
// THE FAILURE. antifailure.dev and www.antifailure.dev are two custom domains
// on one Azure Static Web App. Both are Ready, both answer 200 for every page,
// and neither redirects to the other, because a Static Web Apps route rule
// matches on PATH and the configuration schema has no hostname condition at
// all. site_origin in production.tfvars held one value, the apex. So every call
// the site makes was refused 403 whenever the visitor had arrived on www: the
// analytics beacon, the enterprise contact form, and the careers application
// form. The contact form's own error message told those people "Could not reach
// the server", which is the sentence it shows for a dropped connection.
//
// It was found on a phone, by a person, on the live site. Every check anybody
// had run was green and could not have been anything else: they all asked the
// apex, and the apex was perfect.
//
// WHAT THIS ASKS THAT NOTHING ELSE DID. tools/site/check-tls.sh already knew
// there were two hostnames; it asks each one for its certificate. The tfvars
// knew there was one origin. Nothing compared the two files, and nothing asked
// Azure which hostnames are really bound. Three lists, no relation between
// them, so the disagreement was invisible from every direction.
//
//	origincheck origins            # the tree alone: hostnames.txt vs the tfvars
//	origincheck domains            # Azure: what is really bound to af-site
//	origincheck live --api <url>   # the deployed plane: does it answer each one
//	origincheck all --api <url>    # all three
//
// `origins` needs nothing and runs on every pull request. `domains` needs the
// Azure CLI, signed in. `live` needs the public internet.
//
// EXIT CODES, and the middle one is the point. 0 allowed, 1 refused, 2 the
// check could not tell. A run that cannot reach Azure exits 2 and says which
// question it did not answer. It never exits 0 for a question it did not ask,
// because a check that cannot say no is worse than no check, and a check that
// says yes when it looked at nothing is worse than that.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	hostnamesPath  = "tools/site/hostnames.txt"
	exemptionsPath = "tools/site/hostname-exemptions.tsv"

	// The Static Web App that serves the marketing site, and the resource group
	// it lives in. Named here rather than passed in so that a run with no
	// arguments asks about the real site: a check whose target is a flag is a
	// check somebody points somewhere harmless.
	siteApp   = "af-site"
	siteGroup = "af-web"
)

// The tfvars a deployed control plane is configured from. Both, because staging
// is where the two origin path is exercised before production runs it, and a
// staging file that fell behind would mean the first exercise of a new hostname
// is on the plane a visitor is standing on.
var tfvarsPaths = []string{
	"infra/terraform/stacks/control-plane/production.tfvars",
	"infra/terraform/stacks/control-plane/staging.tfvars",
}

// crossOriginRoute is a route a page on the marketing site calls from a
// browser, with what breaks for a visitor when the origin is refused. The
// sentence is not decoration: a refusal on the beacon is invisible and a
// refusal on a form is a person being told the server is unreachable, and an
// operator reading this output has to be able to tell those apart.
type crossOriginRoute struct {
	path    string
	visible string
}

// Every route on the control plane that answers a cross origin browser.
//
// A SECOND LIST OF THE SAME ROUTES EXISTS and pretending otherwise is how one
// of them rots. www/lib/control-plane-routes.ts holds the site's inventory of
// every control plane route it calls, and tools/routecheck refuses any file
// under www that builds such a URL outside that module, so a call site cannot
// exist without an entry there. That makes the inventory the better source and
// this list the derived one: this is the CORS subset of it.
//
// Nothing in Go or TypeScript compares the two. One is a value in a .ts file
// and the other is a slice in a .go file, so a fifth cross origin route added
// to the inventory would leave this check quietly passing while that route was
// broken on www, which is this bug wearing a different hat.
// TestEveryCrossOriginRouteInTheInventoryIsProbed is what compares them, in
// both directions, and notProbed below is where a route in the inventory that
// is deliberately not probed states why.
//
// The list is also printed on every run: what was NOT checked has to be as
// readable as what was.
var crossOriginRoutes = []crossOriginRoute{
	{"/v1/site/events", "The analytics beacon. A refusal is invisible to the visitor, and silently drops every count from this hostname."},
	{"/v1/leads", "The enterprise contact form. A refusal shows the visitor \"Could not reach the server\", which is the sentence that form shows for a dropped connection."},
	{"/v1/applications", "The careers application form. A refusal shows a failure on a form somebody has just filled in."},
}

// inventoryPath is the site's own list of every control plane route it calls.
// Read by the test that keeps crossOriginRoutes honest against it.
const inventoryPath = "www/lib/control-plane-routes.ts"

// Routes the site's inventory declares that this check deliberately does NOT
// send a preflight to, with the reason each one is allowed to stay that way. A
// reason is required for the same purpose as every other exemption file here:
// an exemption with no argument behind it cannot be told apart from somebody
// silencing a finding they did not understand. A row that stops being needed is
// reported, so this cannot rot into a permanent allowance.
var notProbed = map[string]string{
	"/auth/github": "A top level navigation, not a fetch. The browser follows a link to it and sends no Origin header and no preflight, so there is no CORS answer for this check to assert and no hostname it can be wrong about. It breaks by being absent rather than by refusing an origin, which is exactly what tools/routecheck probes it for.",
}

func main() {
	api := flag.String("api", "https://app.antifailure.dev", "the control plane to ask, for `live`")
	root := flag.String("C", ".", "the repository root")
	flag.Usage = usage
	// Subcommand first, flags after, the same shape azguard uses.
	args := os.Args[1:]
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	command := args[0]
	if err := flag.CommandLine.Parse(args[1:]); err != nil {
		os.Exit(2)
	}
	// A positional root after the subcommand, because every other checker in
	// this repository is invoked as `go run ./tools/<name> .` and muscle memory
	// is a real interface. Without this, `origincheck origins .` exits 2 with a
	// usage screen, which reads like the check refusing rather than like the
	// argument being in the wrong place. -C still works and wins if both are
	// given, since it is what the justfile recipes were written against.
	if flag.NArg() > 0 && *root == "." {
		*root = flag.Arg(0)
	}

	switch command {
	case "origins":
		os.Exit(cmdOrigins(*root))
	case "domains":
		os.Exit(cmdDomains(*root))
	case "live":
		os.Exit(cmdLive(*root, *api))
	case "all":
		os.Exit(worst(cmdOrigins(*root), cmdDomains(*root), cmdLive(*root, *api)))
	case "-h", "--help", "help":
		usage()
		os.Exit(0)
	default:
		// The house convention is `go run ./tools/<name> .`, so somebody will
		// type that here. Say which subcommand they meant rather than printing
		// the whole usage screen at a person who is one word away.
		if command == "." || !strings.HasPrefix(command, "-") {
			fmt.Fprintf(os.Stderr, "origincheck: %q is not a command. This one takes a subcommand first:\n"+
				"  origincheck origins %s      the tree alone, no network and no credential\n"+
				"  origincheck domains %s      what Azure really serves\n"+
				"  origincheck live %s         what a deployed control plane really answers\n"+
				"  origincheck all %s          all three\n", command, command, command, command, command)
			os.Exit(2)
		}
		fmt.Fprintf(os.Stderr, "origincheck: unknown command %q\n", command)
		usage()
		os.Exit(2)
	}
}

// worst is how `all` reports. A refusal outranks a could-not-tell, because a
// known broken hostname is a worse thing to be quiet about than an unasked
// question, and both outrank success.
func worst(codes ...int) int {
	out := 0
	for _, c := range codes {
		if c == 1 {
			return 1
		}
		if c > out {
			out = c
		}
	}
	return out
}

func usage() {
	fmt.Fprint(os.Stderr, `origincheck: every hostname the site is served on is one the control plane answers.

  origincheck origins            The tree alone. Every hostname in
                                 tools/site/hostnames.txt has a matching origin
                                 in each control plane tfvars, and every origin
                                 in those files has a matching hostname. No
                                 network, no credential.

  origincheck domains            Asks Azure which custom domains are bound to
                                 `+siteApp+` in `+siteGroup+`, and refuses when that set is
                                 not what tools/site/hostnames.txt claims. Needs
                                 the Azure CLI, signed in.

  origincheck live --api <url>   Sends a real CORS preflight from each hostname
                                 to each cross origin route on a deployed
                                 control plane, and refuses one that is not
                                 answered. Needs the public internet.

  origincheck all --api <url>    All three.

Flags: -C <root> the repository root, --api <url> the control plane for `+"`live`"+`.

Exit: 0 allowed, 1 refused, 2 the check could not tell.
`)
}

// ---------------------------------------------------------------------------
// The tree
// ---------------------------------------------------------------------------

func cmdOrigins(root string) int {
	hosts, err := hostnames(root)
	if err != nil {
		return cannotTell(err)
	}
	if len(hosts) == 0 {
		return cannotTell(fmt.Errorf("%s names no hostnames. An empty list would make every "+
			"comparison below vacuously true, which is the shape of a check that cannot say no", hostnamesPath))
	}
	fmt.Printf("%s names %d hostname(s): %s\n", hostnamesPath, len(hosts), strings.Join(hosts, ", "))

	failures := 0
	for _, path := range tfvarsPaths {
		origins, err := siteOrigins(root, path)
		if err != nil {
			return cannotTell(err)
		}
		want := map[string]bool{}
		for _, h := range hosts {
			want["https://"+h] = true
		}
		have := map[string]bool{}
		for _, o := range origins {
			have[o] = true
		}
		for _, h := range hosts {
			origin := "https://" + h
			if have[origin] {
				fmt.Printf("  ok   %s allows %s\n", path, origin)
				continue
			}
			// The exact failure this tool was written for, named as what a
			// person experiences rather than as a diff between two files.
			failures++
			fmt.Fprintf(os.Stderr, "  FAIL %s does not allow %s.\n", path, origin)
			fmt.Fprintf(os.Stderr, "       The site answers on %s, so a visitor who arrived there sends\n", h)
			fmt.Fprintf(os.Stderr, "       `origin: %s` and the control plane refuses it 403.\n", origin)
			for _, r := range crossOriginRoutes {
				fmt.Fprintf(os.Stderr, "         %s  %s\n", r.path, r.visible)
			}
			fmt.Fprintf(os.Stderr, "       Add %q to site_origin in %s.\n", origin, path)
			annotate("%s does not allow %s, which the site is served on", path, origin)
		}
		// The other direction, because an origin allowed for a hostname the
		// site is not served on is a widened CORS boundary nobody meant to
		// widen, and it reads exactly like a leftover.
		for _, o := range origins {
			if want[o] {
				continue
			}
			failures++
			fmt.Fprintf(os.Stderr, "  FAIL %s allows %s and the site is not served there.\n", path, o)
			fmt.Fprintf(os.Stderr, "       Every entry widens the set of pages that may post to the\n")
			fmt.Fprintf(os.Stderr, "       unauthenticated write routes. Remove it, or add the hostname to %s\n", hostnamesPath)
			fmt.Fprintf(os.Stderr, "       if the site really is served there.\n")
			annotate("%s allows %s, which is not a hostname the site is served on", path, o)
		}
	}
	if failures > 0 {
		fmt.Fprintf(os.Stderr, "\norigincheck origins: %d disagreement(s) between %s and the control plane configuration.\n",
			failures, hostnamesPath)
		return 1
	}
	fmt.Printf("origincheck origins: every hostname has an allowed origin in every control plane tfvars\n")
	return 0
}

// ---------------------------------------------------------------------------
// Azure
// ---------------------------------------------------------------------------

type customDomain struct {
	DomainName string `json:"domainName"`
	Status     string `json:"status"`
}

func cmdDomains(root string) int {
	claimed, err := hostnames(root)
	if err != nil {
		return cannotTell(err)
	}
	exempt, order, err := exemptions(root)
	if err != nil {
		return cannotTell(err)
	}

	bound, err := boundDomains()
	if err != nil {
		// FAILS CLOSED, and this is the branch the instruction was written
		// about. A check that cannot reach Azure has not established that the
		// hostname list is complete, and reporting that as a pass is how the
		// next hostname somebody binds becomes invisible all over again.
		return cannotTell(fmt.Errorf("could not enumerate the custom domains on %s in %s, so "+
			"NOTHING here was checked: %w\n       Sign in with `az login`, or run this where the "+
			"Azure CLI can reach the subscription", siteApp, siteGroup, err))
	}
	return compareDomains(claimed, exempt, order, bound)
}

// compareDomains is the whole judgement, separated from the call to Azure so
// that it can be driven against a set of domains that does not exist. A rule
// that can only be exercised by whatever is really deployed today is a rule
// whose failing branch is never run: it goes green because the world is
// currently fine, which is indistinguishable from going green because it
// stopped looking.
func compareDomains(claimed []string, exempt map[string]string, order []string, bound []customDomain) int {
	claim := map[string]bool{}
	for _, h := range claimed {
		claim[h] = true
	}
	failures := 0
	seen := map[string]bool{}
	for _, d := range bound {
		host := strings.ToLower(strings.TrimSpace(d.DomainName))
		if host == "" {
			continue
		}
		seen[host] = true
		switch {
		case claim[host]:
			fmt.Printf("  ok   %s is bound to %s and %s names it (status %s)\n", host, siteApp, hostnamesPath, d.Status)
		case exempt[host] != "":
			fmt.Printf("  ok   %s is bound and deliberately not an allowed origin: %s\n", host, exempt[host])
		default:
			failures++
			fmt.Fprintf(os.Stderr, "  FAIL %s is bound to %s (status %s) and %s does not name it.\n",
				host, siteApp, d.Status, hostnamesPath)
			fmt.Fprintf(os.Stderr, "       The site answers there and nothing in this repository knows, so no\n")
			fmt.Fprintf(os.Stderr, "       origin was configured for it and every call a page on it makes is\n")
			fmt.Fprintf(os.Stderr, "       refused 403. Add the hostname to %s and the origin to site_origin,\n", hostnamesPath)
			fmt.Fprintf(os.Stderr, "       or add a row to %s saying why it must not be allowed.\n", exemptionsPath)
			annotate("%s is served by %s and is not in %s", host, siteApp, hostnamesPath)
		}
	}
	// A hostname this repository claims and Azure does not serve. Harmless to a
	// visitor and not harmless to the checks: check-tls.sh would ask it for a
	// certificate forever, and its origin sits in the CORS allowlist for a page
	// that does not exist.
	for _, h := range claimed {
		if seen[h] {
			continue
		}
		failures++
		fmt.Fprintf(os.Stderr, "  FAIL %s names %s and it is not bound to %s.\n", hostnamesPath, h, siteApp)
		fmt.Fprintf(os.Stderr, "       Its origin is allowed to post to the unauthenticated write routes and\n")
		fmt.Fprintf(os.Stderr, "       the site is not served there. Remove the line, or bind the domain.\n")
		annotate("%s names %s and Azure does not serve it", hostnamesPath, h)
	}
	// A row that has stopped being needed, so the exemption file cannot rot
	// into a permanent allowance the way a hand maintained list does.
	for _, host := range order {
		if !seen[host] {
			failures++
			fmt.Fprintf(os.Stderr, "  FAIL %s exempts %s and it is no longer bound to %s, so the row can go.\n",
				exemptionsPath, host, siteApp)
			annotate("%s has a stale row for %s", exemptionsPath, host)
		}
	}

	if failures > 0 {
		fmt.Fprintf(os.Stderr, "\norigincheck domains: %d disagreement(s) between Azure and %s.\n", failures, hostnamesPath)
		return 1
	}
	fmt.Printf("origincheck domains: the %d domain(s) bound to %s are exactly what %s claims\n",
		len(bound), siteApp, hostnamesPath)
	return 0
}

// boundDomains asks Azure. It reads the custom domains AND the platform's own
// default hostname, because the default hostname serves the same site: a page
// loaded from it is a page whose beacon and forms are refused, and leaving it
// out of the enumeration would be choosing not to see one of the hostnames the
// site really answers on.
func boundDomains() ([]customDomain, error) {
	out, err := run("az", "staticwebapp", "hostname", "list", "-n", siteApp, "-g", siteGroup, "-o", "json")
	if err != nil {
		return nil, err
	}
	var domains []customDomain
	if err := json.Unmarshal([]byte(out), &domains); err != nil {
		return nil, fmt.Errorf("could not read the custom domain list as JSON: %w", err)
	}
	defaultHost, err := run("az", "staticwebapp", "show", "-n", siteApp, "-g", siteGroup,
		"--query", "defaultHostname", "-o", "tsv")
	if err != nil {
		return nil, err
	}
	if h := strings.TrimSpace(defaultHost); h != "" {
		domains = append(domains, customDomain{DomainName: h, Status: "default hostname"})
	}
	if len(domains) == 0 {
		return nil, fmt.Errorf("%s in %s reports no hostnames at all, not even a default one, "+
			"which is not a state this app can be in and reads as a parse failure", siteApp, siteGroup)
	}
	return domains, nil
}

func run(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), msg)
	}
	return string(out), nil
}

// ---------------------------------------------------------------------------
// The deployed control plane
// ---------------------------------------------------------------------------

func cmdLive(root, api string) int {
	hosts, err := hostnames(root)
	if err != nil {
		return cannotTell(err)
	}
	api = strings.TrimRight(api, "/")
	fmt.Printf("asking %s whether it answers each hostname, on %d cross origin route(s):\n", api, len(crossOriginRoutes))
	for _, r := range crossOriginRoutes {
		fmt.Printf("  %s\n", r.path)
	}
	fmt.Printf("A route not listed above was NOT checked.\n\n")

	client := &http.Client{Timeout: 20 * time.Second}
	failures, unknown := 0, 0
	for _, host := range hosts {
		origin := "https://" + host
		for _, route := range crossOriginRoutes {
			status, allow, err := preflight(client, api+route.path, origin)
			switch {
			case err != nil:
				unknown++
				fmt.Fprintf(os.Stderr, "  ????  %s %s: NOT CHECKED, the request itself failed: %v\n", origin, route.path, err)
			case status == http.StatusNotFound:
				// The route does not exist on this deployment. That is a real
				// defect and it is a DIFFERENT one: merging to main deploys
				// staging only, so a front end can ship a route the tagged
				// production image does not serve. Counted as unknown rather
				// than as a refusal, because the origin was never tested, and
				// never as a pass.
				unknown++
				fmt.Fprintf(os.Stderr, "  ????  %s %s: NOT CHECKED, this plane answers 404, so the route is not\n", origin, route.path)
				fmt.Fprintf(os.Stderr, "        deployed here and the origin was never compared. That is a deploy\n")
				fmt.Fprintf(os.Stderr, "        lag rather than an origin refusal, and it is not a pass.\n")
			case allow == origin:
				fmt.Printf("  ok    %s %s: %d, allow-origin echoes this origin\n", origin, route.path, status)
			case allow == "":
				failures++
				fmt.Fprintf(os.Stderr, "  FAIL  %s %s: %d with no access-control-allow-origin.\n", origin, route.path, status)
				fmt.Fprintf(os.Stderr, "        A browser on %s refuses the response. %s\n", host, route.visible)
				annotate("%s refuses a preflight from %s on %s", api, origin, route.path)
			default:
				// The header carried a different origin. A browser compares it
				// against its own origin and refuses, so this is a refusal that
				// reads like a success in any tool that only checks presence.
				failures++
				fmt.Fprintf(os.Stderr, "  FAIL  %s %s: %d but allow-origin is %q, not this origin.\n",
					origin, route.path, status, allow)
				fmt.Fprintf(os.Stderr, "        The browser compares that against its own origin and refuses.\n")
				annotate("%s echoes %s for a request from %s on %s", api, allow, origin, route.path)
			}
		}
	}
	fmt.Println()
	if failures > 0 {
		fmt.Fprintf(os.Stderr, "origincheck live: %d hostname/route pair(s) the control plane will not answer", failures)
		if unknown > 0 {
			fmt.Fprintf(os.Stderr, ", and %d it could not be asked about", unknown)
		}
		fmt.Fprintln(os.Stderr, ".")
		return 1
	}
	if unknown > 0 {
		fmt.Fprintf(os.Stderr, "origincheck live: %d hostname/route pair(s) NOT CHECKED. Nothing here is a pass.\n", unknown)
		return 2
	}
	fmt.Printf("origincheck live: %s answers every hostname on every cross origin route listed above\n", api)
	return 0
}

// preflight sends what a browser sends before a cross origin POST, and reads
// back the one header that decides whether the browser will make the real
// request. Not a GET: a GET is not the request that was failing, and a route
// can answer a GET perfectly while refusing every preflight.
func preflight(client *http.Client, url, origin string) (int, string, error) {
	req, err := http.NewRequest(http.MethodOptions, url, nil)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("origin", origin)
	req.Header.Set("access-control-request-method", "POST")
	req.Header.Set("access-control-request-headers", "content-type")
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	return resp.StatusCode, resp.Header.Get("access-control-allow-origin"), nil
}

// ---------------------------------------------------------------------------
// Reading the tree
// ---------------------------------------------------------------------------

func hostnames(root string) ([]string, error) {
	path := filepath.Join(root, hostnamesPath)
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("could not read %s: %w", hostnamesPath, err)
	}
	var hosts []string
	seen := map[string]bool{}
	for i, line := range strings.Split(string(b), "\n") {
		if idx := strings.Index(line, "#"); idx >= 0 {
			line = line[:idx]
		}
		host := strings.ToLower(strings.TrimSpace(line))
		if host == "" {
			continue
		}
		// A hostname, not an origin and not a URL. Accepting https://host here
		// would let the file and the tfvars drift into two different notations
		// for the same thing, and the comparison below would stop matching
		// without ever saying so.
		if strings.ContainsAny(host, "/:") {
			return nil, fmt.Errorf("%s:%d is %q. One hostname per line, with no scheme and no path", hostnamesPath, i+1, host)
		}
		if seen[host] {
			return nil, fmt.Errorf("%s:%d names %s twice", hostnamesPath, i+1, host)
		}
		seen[host] = true
		hosts = append(hosts, host)
	}
	return hosts, nil
}

func exemptions(root string) (map[string]string, []string, error) {
	path := filepath.Join(root, exemptionsPath)
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]string{}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	reasons := map[string]string{}
	var order []string
	for i, line := range strings.Split(string(b), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		// A reason is required, because an exemption with no argument behind it
		// cannot be told apart from somebody silencing a finding they did not
		// understand.
		if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
			return nil, nil, fmt.Errorf("%s:%d has no reason. Two tab separated fields, the "+
				"hostname and why its origin must not be allowed", exemptionsPath, i+1)
		}
		host := strings.ToLower(strings.TrimSpace(parts[0]))
		if _, dup := reasons[host]; dup {
			return nil, nil, fmt.Errorf("%s:%d names %s twice", exemptionsPath, i+1, host)
		}
		reasons[host] = strings.TrimSpace(parts[1])
		order = append(order, host)
	}
	return reasons, order, nil
}

// siteOrigins reads the site_origin value out of a tfvars file and splits it.
//
// One string carrying several comma separated origins, which is the shape the
// process reads: AF_SITE_ORIGIN is set to this value with nothing done to it,
// so what an operator writes and what siteOriginsFrom parses are the same bytes.
// It stays a string rather than becoming a list because it is an input
// docs/reference/stability.md promised, and tools/inputcheck refuses a variable
// that is renamed or retyped.
//
// A hand written reader rather than an HCL parser, and the risk of that is a
// pattern that stops matching and reports nothing while looking clean. So it
// refuses rather than returning an empty list when the assignment is absent
// altogether: an absent variable and an empty one are different states and only
// one of them is somebody deliberately refusing every origin.
func siteOrigins(root, rel string) ([]string, error) {
	path := filepath.Join(root, rel)
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("could not read %s: %w", rel, err)
	}
	for i, line := range strings.Split(string(b), "\n") {
		code := strings.TrimSpace(stripComment(line))
		eq := strings.Index(code, "=")
		if eq < 0 || strings.TrimSpace(code[:eq]) != "site_origin" {
			continue
		}
		value := strings.TrimSpace(code[eq+1:])
		if len(value) < 2 || !strings.HasPrefix(value, `"`) || !strings.HasSuffix(value, `"`) {
			return nil, fmt.Errorf("%s:%d assigns site_origin something that is not a quoted string: %q", rel, i+1, value)
		}
		value = value[1 : len(value)-1]
		if strings.TrimSpace(value) == "" {
			return nil, nil
		}
		var origins []string
		for _, field := range strings.Split(value, ",") {
			field = strings.ToLower(strings.TrimSpace(field))
			if field == "" {
				// The application stops the process on a stray comma rather
				// than dropping it, because ",,," would otherwise read as a
				// configured list. Reported here for the same reason.
				return nil, fmt.Errorf("%s:%d has an empty entry in site_origin: %q. A stray comma is a "+
					"typo, and the control plane refuses to start on one", rel, i+1, value)
			}
			origins = append(origins, field)
		}
		return origins, nil
	}
	return nil, fmt.Errorf("%s does not assign site_origin at all. An absent assignment is not an "+
		"empty one: it means this plane refuses every beacon, lead and application, and if that is "+
		"deliberate it has to be written down as site_origin = \"\"", rel)
}

func stripComment(line string) string {
	if idx := strings.Index(line, "#"); idx >= 0 {
		return line[:idx]
	}
	return line
}

// ---------------------------------------------------------------------------

func cannotTell(err error) int {
	fmt.Fprintf(os.Stderr, "origincheck: %v\n", err)
	fmt.Fprintf(os.Stderr, "origincheck: NOT CHECKED. This is not a pass.\n")
	annotate("origincheck could not tell: %v", err)
	return 2
}

// annotate puts a finding where a person reading a workflow run will see it,
// and does nothing anywhere else.
func annotate(format string, args ...any) {
	if os.Getenv("GITHUB_ACTIONS") == "" {
		return
	}
	fmt.Printf("::error::%s\n", fmt.Sprintf(format, args...))
}
