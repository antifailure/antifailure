// Command wirecheck proves that a variable the control plane's reference
// documents can actually be DELIVERED by the deploy path this product supports.
//
// THE FAILURE IT WAS WRITTEN FOR. On the day it was written the reference
// documented 45 environment variables the hosted control plane reads, and the
// Terraform module that is the only route onto that container could set 16.
// Twenty nine of them had no way to be set at all, and nothing anywhere said
// so. The staging container's environment held twelve names.
//
// That gap is not read as configuration. Every one of its symptoms reads as a
// broken feature: twenty three operator routes answering "this installation has
// no operator database credential configured"; no sign-in link and no
// invitation able to leave the building; billing OFF with a real Stripe price
// sitting behind Team; the marketing site's beacon refused cross origin as a
// bare network error; the analytics stream recording nothing; and both actions
// missing from the "No organization yet" screen, which is the one somebody
// eventually chased down by hand.
//
// WHY THE TWO CHECKS ALREADY HERE COULD NOT SEE IT, and this is the whole point
// of adding a third rather than widening one of them. tools/varcheck proves
// every variable the product NAMES AT A USER is documented.
// web/apps/api/test/config-docs.test.ts proves the control plane's variables are
// documented. Both are documentation coverage, and a variable that is
// documented, read by the application, and unreachable by every apply passes
// both of them cleanly. They answer a NEARBY question, which is how twenty nine
// accumulated in silence: the instruments were green and could not have been
// anything else.
//
// So this asks the question neither of them does, and it can say no. For every
// variable the reference documents as settable on a hosted installation,
// EITHER the Terraform module can set it, OR a row in
// tools/docs/wiring-exemptions.tsv states why it cannot.
//
// IT ASKS IN BOTH DIRECTIONS, because the reverse is the same defect wearing
// different clothes. A module that sets AF_SITE_ORIGN would deliver a variable
// nothing reads, and the deployment would look configured and behave exactly as
// it did before. A name here that the reference does not carry is reported.
//
// THE EXEMPTION FILE IS THE IMPORTANT HALF, and it is the mechanism
// tools/docs/figure-exemptions.tsv and tools/docs/variable-exemptions.tsv
// already use. A row STATES A REASON, because an exemption with no argument
// behind it cannot be told apart from somebody silencing a finding they did not
// understand. A row that has stopped being needed is reported, so the file
// cannot rot into a permanent allowance the way a hand-maintained list does.
//
// Several variables genuinely belong there rather than in Terraform, and
// getting those right is as much of the work as getting the wiring right.
// AF_VERSION and AF_COMMIT are stamped into the image at build time and setting
// them by hand only makes /readyz lie. AF_MIGRATE is absent from the serving
// process on purpose, because migrations racing across replicas at start-up is
// a worse way to apply a schema than a job that runs once.
//
// WHAT IT DOES NOT PROVE, AND THIS PARAGRAPH IS NOT A DISCLAIMER. It is the
// same mistake this tool was written about, aimed at this tool.
//
// It proves a variable can be DELIVERED. It does not prove the feature works.
// Those are different sentences and the distance between them is exactly where
// the last finding lived: varcheck and config-docs.test.ts proved a variable
// was DOCUMENTED, everybody read them as proving it was USABLE, and twenty nine
// unreachable variables sat under two green checks for months.
//
// The live example is mail. This gate will go green on AF_MAIL_FROM and
// AF_RESEND_API_KEY the moment the module can set them, and mail sent from
// antifailure.dev is still rejected by every receiver that honours DMARC: the
// domain publishes `v=spf1 -all`, which authorises no sender, a DMARC policy of
// p=reject with strict alignment, and a Resend DKIM record whose empty `p=` is
// a REVOKED key. Nothing in this repository can see that and nothing in this
// repository should pretend to. It is DNS, and self-hosting/azure.md names it
// as the step before AF_MAIL_FROM is worth setting.
//
// Nor is "can be set" the same as "is set". Whether a particular installation
// sets a variable is a tfvars file's decision, and this tool never reads one,
// deliberately: a tfvars file is the operator's and lives in their repository.
//
// So the claim is narrow, and it is the one that was false: for every variable
// the reference documents, a supported deploy path exists that can deliver it.
// Read it as anything wider and this gate becomes the third one answering a
// nearby question.
//
// It also reads the module and not the stack. The stack passes inputs through
// to the module, and the module is what owns the container template, so the
// module is where a variable becomes reachable or does not. A stack that
// forgets a pass-through is caught by tools/inputcheck's snapshot instead.
//
// THE SECOND ROUTE, AND THE FINDING THAT ADDED IT. Everything above was written
// about Terraform, because Terraform was the deploy path this tool knew. It is
// not the only one this product supports. deploy/helm/antifailure-control-plane
// installs the same control plane on Kubernetes, and on the day this paragraph
// was written that chart could set 15 of the 47 variables the reference
// documents. Thirty two were unreachable from a Kubernetes installation, with
// no generic escape hatch either, and this gate was green throughout, because
// its question named Terraform.
//
// That is the ORIGINAL FAILURE ARRIVING FROM A DIRECTION THE FIX DID NOT COVER.
// AF_SITE_ORIGIN is the one somebody noticed, because its symptom is loud and
// specific: a marketing site's enterprise contact form answers "Could not reach
// the server. Check your connection and press it again", which blames the
// visitor's network for a refusal the deployment made on purpose, and records
// no lead. It was not special. The operator database credential, the whole
// GitHub App, all of billing, all of mail, provider key sealing and the entire
// analytics pipeline were in exactly the same hole, and every one of them
// presents as a broken feature rather than as configuration.
//
// So a route is now a thing this tool has a list of, and a variable has to be
// deliverable by EVERY supported route or carry a reason per route. The
// exemption file grew a target column for that, and the column is doing real
// work rather than bookkeeping: AF_INSECURE_COOKIES is refused by the Terraform
// module on purpose, because TLS terminates at the Container Apps ingress and
// setting it would send session cookies without the Secure attribute over a
// public route, and the chart sets it deliberately, because a chart installs on
// a laptop over plain HTTP with no ingress at all. One variable, two routes, two
// opposite and both correct answers. A single-column file could hold only one
// of them.
//
// A MENTION IS NOT A USE, and the chart reader is where that bites here.
// deployment.yaml carries the line "AF_MIGRATE is deliberately absent" in a
// comment, so a scan for the string AF_MIGRATE in that file finds it and
// concludes the chart delivers the one variable the chart is stating it refuses
// to deliver. The reader therefore drops comments, both YAML and Helm, and
// matches a name only in the position where an env entry names one. It is the
// same discipline the reference parser above uses when it counts only the first
// cell of a table row, and the general rule is worth stating once:
//
//	A check that matches TEXT while its claim is about a POSITION in a
//	structure cannot tell a mention from a use. Parse the position where you
//	can, narrow the input until every occurrence in it is a use where you
//	cannot, and reach for a list of exceptions last, because an exemption is a
//	sentence somebody has to keep being right about.
//
// The exemption file below is the last of those three, used where it is the
// honest answer: the reason a route cannot deliver a variable is not visible in
// any structure, it has to be argued, and the argument is what the row is.
//
// WHAT DOES NOT COUNT AS DELIVERY, and this is a decision rather than an
// oversight. The chart also gained a generic `extraEnv`, which puts arbitrary
// entries into the container, and this tool ignores it. If it counted, the
// chart would be permanently green for every variable that will ever exist,
// including the next one somebody forgets, which is a check that cannot say no.
// A generic hatch makes every variable reachable and none of them discoverable;
// a named value is the chart telling an operator what there is to set. The
// chart carries both, on purpose, and only the named half is delivery.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	referencePath  = "docs/src/content/docs/reference/control-plane.md"
	modulePath     = "infra/terraform/modules/control-plane"
	chartPath      = "deploy/helm/antifailure-control-plane"
	exemptionsPath = "tools/docs/wiring-exemptions.tsv"

	// The exemption file's second field. A row says which ROUTE it excuses,
	// because a reason true of one is routinely false of the other.
	targetTerraform = "terraform"
	targetHelm      = "helm"

	// HOW THE PAGE SAYS "THIS ONE IS NOT SET HERE", read off the table itself.
	//
	// Some variables on this page are not the control plane's environment to
	// carry. AF_CONTROL_PLANE_TOKEN is issued and verified by the control plane
	// and never read from its own environment; AF_ADMIN_BOOTSTRAP_PASSWORD is
	// typed into the shell that runs a command and the serving process never
	// reads it. The page declares both the same way: their tables carry a
	// "Where it is set" column instead of a "Default" one, and the cell answers
	// it -- "On the engine, or in a CI job", "In the shell that runs the
	// command".
	//
	// Keyed on that column rather than on the section titles, and the reason is
	// not tidiness. This gate first read the titles, and the very next thing
	// that happened was a new section, "Read by a command, not by the server",
	// arriving on main with a variable under it. The gate went red, correctly,
	// but a rule that needs a new constant for every heading somebody writes is
	// a rule that will one day be widened by whoever is in a hurry. The column
	// is the page's own statement about the row, it travels with the table, and
	// a section written in that shape tomorrow needs nothing added here.
	//
	// It fails in the safe direction. Rename the column and these variables
	// become ordinary and demand an env block or a row, which is loud. There is
	// no spelling of it that makes a variable disappear from the gate quietly.
	notSetHereColumn = "Where it is set"

	// A FLOOR, because a parser that stops matching looks exactly like a
	// codebase with nothing to find. Both numbers are far below what the two
	// surfaces really hold; they exist so that a silent parse failure fails the
	// gate instead of passing it.
	minDocumented    = 20
	minSettable      = 10
	minChartSettable = 10
)

var (
	varName = regexp.MustCompile(`AF_[A-Z0-9_]+`)
	// The name argument inside an env block. Terraform's own formatter aligns
	// the equals sign, so the spacing is not fixed.
	envName = regexp.MustCompile(`^\s*name\s*=\s*"(AF_[A-Z0-9_]+)"\s*$`)
	// An env block opens as either `env {` or `dynamic "env" {`.
	envOpen = regexp.MustCompile(`^\s*(?:dynamic\s+"env"|env)\s*\{\s*$`)
	// A markdown table row. The separator row is every cell dashes.
	tableSeparator = regexp.MustCompile(`^\|[\s:|-]+\|$`)
	// An entry in a Kubernetes container's env list. The POSITION, not the
	// name: a name anywhere else on the line, or in prose, is a mention.
	chartEnvName = regexp.MustCompile(`^\s*-\s*name:\s*"?(AF_[A-Z0-9_]+)"?\s*$`)
)

type site struct{ name, where string }

// A supported way to install this control plane, and what it can put into the
// container it starts.
//
// A LIST rather than two variables, because the failure this tool exists for is
// a route nobody asked the question about. Terraform was the only route it knew
// and a whole Kubernetes installation was invisible for that reason alone. A
// third route added here has to answer the same question the moment it is
// added, and the exemption file's target column has to learn its key.
type route struct {
	// The exemption file's second field.
	key string
	// How it is named in a message a person reads.
	what string
	// Where somebody goes to fix it.
	where string
	// How the fix is spelled there.
	fix string
	// Variable to the line that delivers it.
	sets map[string]string
	// Below this, the reader is presumed broken rather than the route empty.
	floor int
}

func main() {
	list := flag.Bool("list", false, "print every documented variable and where it is delivered, then exit")
	exemptOnly := flag.Bool("exemptions", false, "print the exemption file's effective rows and exit")
	flag.Parse()
	root := "."
	if flag.NArg() > 0 {
		root = flag.Arg(0)
	}

	documented, err := documentedVariables(root)
	if err != nil {
		fail(err)
	}
	terraformSets, err := settableVariables(root)
	if err != nil {
		fail(err)
	}
	chartSets, err := chartVariables(root)
	if err != nil {
		fail(err)
	}
	routes := []route{
		{
			key: targetTerraform, what: "the Terraform module", where: modulePath,
			fix:  "an env block",
			sets: terraformSets, floor: minSettable,
		},
		{
			key: targetHelm, what: "the Helm chart", where: chartPath,
			fix:  "a named value and an env entry",
			sets: chartSets, floor: minChartSettable,
		},
	}
	exempt, order, err := exemptions(root, routes)
	if err != nil {
		fail(err)
	}
	if err := floors(documented, routes); err != nil {
		fail(err)
	}

	if *exemptOnly {
		for _, row := range order {
			fmt.Printf("%s\t%s\t%s\n", row.name, row.target, exempt[row.target][row.name])
		}
		return
	}
	if *list {
		for _, n := range sortedKeys(documented) {
			for _, r := range routes {
				switch {
				case r.sets[n] != "":
					fmt.Printf("%s\t%s\t%s\n", n, r.key, r.sets[n])
				case exempt[r.key][n] != "":
					fmt.Printf("%s\t%s\texempt\t%s\n", n, r.key, exempt[r.key][n])
				default:
					fmt.Printf("%s\t%s\tUNREACHABLE\t%s\n", n, r.key, documented[n])
				}
			}
		}
		return
	}

	// A variable this route can neither set nor argue its way out of. Reported
	// per route, because "unreachable on Kubernetes" and "unreachable on Azure"
	// are two different bugs with two different fixes and a combined count
	// hides which one you have.
	undeliverable := map[string][]site{}
	var undocumented []site
	for _, r := range routes {
		for _, n := range sortedKeys(documented) {
			if r.sets[n] != "" || exempt[r.key][n] != "" {
				continue
			}
			undeliverable[r.key] = append(undeliverable[r.key], site{n, documented[n]})
		}
		for _, n := range sortedKeys(r.sets) {
			if _, ok := documented[n]; ok {
				continue
			}
			undocumented = append(undocumented, site{n, r.sets[n]})
		}
	}

	// A row that is no longer needed. The same half figurecheck and varcheck
	// have, and the reason they have it: staleness that is not reported is an
	// allowance that outlives its argument.
	byKey := map[string]route{}
	for _, r := range routes {
		byKey[r.key] = r
	}
	var stale []string
	for _, row := range order {
		r := byKey[row.target]
		switch {
		case documented[row.name] == "":
			stale = append(stale, fmt.Sprintf(
				"%s is exempt on %s and %s no longer documents it, so the row can go",
				row.name, row.target, referencePath))
		case r.sets[row.name] != "":
			stale = append(stale, fmt.Sprintf(
				"%s is exempt on %s and %s now sets it at %s, so the row can go",
				row.name, row.target, r.what, r.sets[row.name]))
		}
	}

	missing := 0
	for _, r := range routes {
		missing += len(undeliverable[r.key])
	}
	if missing == 0 && len(undocumented) == 0 && len(stale) == 0 {
		fmt.Printf("wirecheck: %d variables documented, deliverable by %d installation routes\n",
			len(documented), len(routes))
		for _, r := range routes {
			fmt.Printf("  %-9s %d set by %s, %d exempt with a reason\n",
				r.key, countReachable(documented, r.sets), r.what, len(exempt[r.key]))
		}
		return
	}

	for _, r := range routes {
		for _, f := range undeliverable[r.key] {
			fmt.Fprintf(os.Stderr,
				"  %s is documented at %s and %s cannot set it\n", f.name, f.where, r.what)
		}
	}
	for _, f := range undocumented {
		fmt.Fprintf(os.Stderr,
			"  %s is set at %s and %s does not document it\n", f.name, f.where, referencePath)
	}
	for _, st := range stale {
		fmt.Fprintf(os.Stderr, "  %s\n", st)
	}
	fmt.Fprintf(os.Stderr,
		"\nwirecheck: %d documented with no way to set them, %d set and undocumented, %d stale exemptions.\n",
		missing, len(undocumented), len(stale))
	for _, r := range routes {
		if len(undeliverable[r.key]) == 0 {
			continue
		}
		fmt.Fprintf(os.Stderr,
			"For %s: add %s in %s, or a row to %s with %s in the second field saying why it cannot be set there.\n",
			r.what, r.fix, r.where, exemptionsPath, r.key)
	}
	os.Exit(1)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "wirecheck:", err)
	os.Exit(1)
}

func countReachable(documented, settable map[string]string) int {
	n := 0
	for name := range documented {
		if settable[name] != "" {
			n++
		}
	}
	return n
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// documentedVariables maps each variable the reference documents as settable on
// this installation to where it says so.
//
// A variable counts when it is the FIRST cell of a markdown table row, which is
// the shape every one of these tables uses and is what separates a row defining
// a variable from a sentence mentioning one. The prose around the tables names
// plenty of variables in passing, and a mention is not a definition.
func documentedVariables(root string) (map[string]string, error) {
	path := filepath.Join(root, referencePath)
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	// Reset at every heading, so the shape of one table never leaks into the
	// next: a section that opens with no table of its own must not inherit the
	// previous section's answer.
	inNotSetHereTable := false
	for i, line := range strings.Split(string(b), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "#") {
			inNotSetHereTable = false
			continue
		}
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		if tableSeparator.MatchString(strings.TrimSpace(line)) {
			continue
		}
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		if len(cells) == 0 {
			continue
		}
		first := strings.TrimSpace(cells[0])
		// A header row, which is where the table declares its own shape.
		if first == "Variable" {
			inNotSetHereTable = len(cells) > 1 && strings.TrimSpace(cells[1]) == notSetHereColumn
			continue
		}
		if inNotSetHereTable {
			continue
		}
		// Backticked and nothing else, so that a cell of prose that happens to
		// begin with a variable name is not read as a definition.
		if !strings.HasPrefix(first, "`") || !strings.HasSuffix(first, "`") {
			continue
		}
		name := strings.Trim(first, "`")
		if !varName.MatchString(name) || varName.FindString(name) != name {
			continue
		}
		if _, seen := out[name]; !seen {
			out[name] = fmt.Sprintf("%s:%d", referencePath, i+1)
		}
	}
	return out, nil
}

// settableVariables maps each variable the Terraform module can put into a
// container to the env block that does it.
//
// Read by tracking brace depth from an `env {` or `dynamic "env" {` line rather
// than by matching a whole block with one expression, for the reason this
// repository's contract opens with: half of these blocks are dynamic and carry
// a `for_each` and a nested `content` block, so a shallow read either stops
// early or swallows the next block, and both of those failures are silent.
//
// Every .tf file in the module, not only app.tf: the bootstrap job and the
// maintenance job are containers this module configures too, and
// AF_MIGRATION_DATABASE_URL is deliberately delivered to one of them and
// deliberately not to the serving process.
func settableVariables(root string) (map[string]string, error) {
	dir := filepath.Join(root, modulePath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	files := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tf") {
			continue
		}
		files++
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		depth := 0
		for i, line := range strings.Split(string(b), "\n") {
			line = strings.TrimRight(line, "\r")
			if depth == 0 {
				if envOpen.MatchString(line) {
					depth = 1
				}
				continue
			}
			if m := envName.FindStringSubmatch(line); m != nil {
				if _, seen := out[m[1]]; !seen {
					out[m[1]] = fmt.Sprintf("%s/%s:%d", modulePath, e.Name(), i+1)
				}
			}
			depth += strings.Count(line, "{") - strings.Count(line, "}")
			if depth <= 0 {
				depth = 0
			}
		}
	}
	if files == 0 {
		return nil, fmt.Errorf("no .tf files under %s", modulePath)
	}
	return out, nil
}

// floors refuses a run whose parsers found implausibly little.
//
// Separated from the parsers so that they can be read on a three line fixture,
// and kept because a parser that stops matching produces a CLEAN PASS: zero
// documented variables and zero settable ones agree with each other perfectly.
// Both numbers are far below what the two surfaces really hold. They are not a
// measurement of anything, they are a tripwire on the reading.
func floors(documented map[string]string, routes []route) error {
	if len(documented) < minDocumented {
		return fmt.Errorf(
			"read only %d variables out of %s, which is below the floor of %d. "+
				"The tables are probably not the shape this parses; a check that "+
				"cannot find anything looks exactly like a page with nothing in it",
			len(documented), referencePath, minDocumented)
	}
	for _, r := range routes {
		if len(r.sets) < r.floor {
			return fmt.Errorf(
				"found only %d environment variables in %s, which is below the floor of %d. "+
					"The env entries are probably not the shape this parses",
				len(r.sets), r.where, r.floor)
		}
	}
	return nil
}

// exemptions returns the reason per route per variable, and the file's order so
// that a stale row is reported where a reader will find it.
//
// THREE fields now, and the middle one is the reason this was changed rather
// than a second file added. A reason is a claim about ONE installation route
// and is routinely false of another: AF_INSECURE_COOKIES is refused by the
// Terraform module because TLS terminates at its ingress, and set by the Helm
// chart because a chart installs on a laptop over plain HTTP. Two rows, two
// opposite reasons, both true. A file with one row per variable would have to
// pick one of them and would then be silently wrong about the other route.
//
// An unknown route key is refused rather than ignored. A row nothing reads is
// an exemption that looks applied and is not, which is the same shape of defect
// as a stale row and fails just as quietly.
func exemptions(root string, routes []route) (map[string]map[string]string, []exemptRow, error) {
	known := map[string]bool{}
	var keys []string
	for _, r := range routes {
		known[r.key] = true
		keys = append(keys, r.key)
	}
	reasons := map[string]map[string]string{}
	for _, r := range routes {
		reasons[r.key] = map[string]string{}
	}

	path := filepath.Join(root, exemptionsPath)
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return reasons, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var order []exemptRow
	for i, line := range strings.Split(string(b), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 || strings.TrimSpace(parts[2]) == "" {
			return nil, nil, fmt.Errorf("%s:%d has no reason. Three tab separated fields, "+
				"the variable, the installation route it excuses (%s), and why that route "+
				"cannot set it", exemptionsPath, i+1, strings.Join(keys, " or "))
		}
		name := strings.TrimSpace(parts[0])
		target := strings.TrimSpace(parts[1])
		if !known[target] {
			return nil, nil, fmt.Errorf("%s:%d excuses %q on route %q, which nothing reads. "+
				"The second field has to be one of %s, or the row silences nothing while "+
				"looking as though it does", exemptionsPath, i+1, name, target, strings.Join(keys, " or "))
		}
		if _, dup := reasons[target][name]; dup {
			return nil, nil, fmt.Errorf("%s:%d names %s on %s twice", exemptionsPath, i+1, name, target)
		}
		reasons[target][name] = strings.TrimSpace(parts[2])
		order = append(order, exemptRow{name: name, target: target})
	}
	return reasons, order, nil
}

// One row of the exemption file: a variable, and the route it is excused on.
type exemptRow struct{ name, target string }

// chartVariables maps each variable the Helm chart can put into a container to
// the line that does it.
//
// THE POSITION, NOT THE NAME. A chart template is Go template source, not YAML,
// so it cannot be parsed as YAML and a naive search for the string is what is
// left. A naive search is wrong here in a way that is not hypothetical:
// templates/deployment.yaml states, in a comment, that "AF_MIGRATE is
// deliberately absent", and a scan for the string finds it and reports that the
// chart delivers the one variable that file is refusing to deliver. The
// deliberate refusal and the delivery look identical to a grep.
//
// So a name counts only in the POSITION where a container's env list names one:
// a line that is nothing but an env entry's name key. That anchor is what
// excludes a YAML # comment, and there is no separate rule for one, because a #
// comment is a single line and a single line cannot both begin with # and begin
// with the "- name:" the anchor requires. A commented out env entry is excluded
// by the same anchor rather than by a rule about comments.
//
// A Helm {{/* ... */}} block is different and DOES need its own rule, which is
// the one below. It spans lines, so a block explaining how to add a variable
// can contain a line that is exactly an env entry, indented and all, and the
// anchor would match it. Charts document themselves that way constantly. That
// rule was written after the # rule beside it turned out to be unreachable and
// was removed: a stripper that cannot be made to fail is not caution, it is a
// line nobody can check.
//
// extraEnv contributes nothing here and that is deliberate rather than
// incidental. It renders through toYaml, so it carries no literal name for this
// to find even if it were meant to count, and the header says at length why it
// must not count.
//
// Every template, not only deployment.yaml: the bootstrap Job and the
// maintenance CronJob are containers this chart configures too, and
// AF_MIGRATION_DATABASE_URL is deliberately delivered to one of them and
// deliberately not to the serving process.
func chartVariables(root string) (map[string]string, error) {
	dir := filepath.Join(root, chartPath, "templates")
	out := map[string]string{}
	files := 0
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := filepath.Ext(d.Name())
		if ext != ".yaml" && ext != ".yml" && ext != ".tpl" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files++
		// filepath.Rel rather than slicing off len(root): filepath.Join cleans
		// a "." root away, so the slice cut two characters off every path it
		// reported and pointed a reader at a directory that does not exist.
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		inTemplateComment := false
		for i, line := range strings.Split(string(b), "\n") {
			line = strings.TrimRight(line, "\r")
			if inTemplateComment {
				if strings.Contains(line, "*/}}") {
					inTemplateComment = false
				}
				continue
			}
			// Opened and closed on one line is a comment that spans nothing;
			// opened and left open swallows every line until it closes.
			if k := strings.Index(line, "{{/*"); k >= 0 {
				if !strings.Contains(line[k:], "*/}}") {
					inTemplateComment = true
				}
				line = line[:k]
			}
			m := chartEnvName.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			if _, seen := out[m[1]]; !seen {
				out[m[1]] = fmt.Sprintf("%s:%d", rel, i+1)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if files == 0 {
		return nil, fmt.Errorf("no templates under %s/templates", chartPath)
	}
	return out, nil
}
