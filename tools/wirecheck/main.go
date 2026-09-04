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
	exemptionsPath = "tools/docs/wiring-exemptions.tsv"

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
	minDocumented = 20
	minSettable   = 10
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
)

type site struct{ name, where string }

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
	settable, err := settableVariables(root)
	if err != nil {
		fail(err)
	}
	exempt, order, err := exemptions(root)
	if err != nil {
		fail(err)
	}
	if err := floors(documented, settable); err != nil {
		fail(err)
	}

	if *exemptOnly {
		for _, name := range order {
			fmt.Printf("%s\t%s\n", name, exempt[name])
		}
		return
	}
	if *list {
		names := sortedKeys(documented)
		for _, n := range names {
			switch {
			case settable[n] != "":
				fmt.Printf("%s\tterraform\t%s\n", n, settable[n])
			case exempt[n] != "":
				fmt.Printf("%s\texempt\t%s\n", n, exempt[n])
			default:
				fmt.Printf("%s\tUNREACHABLE\t%s\n", n, documented[n])
			}
		}
		return
	}

	var undeliverable, undocumented []site
	for _, n := range sortedKeys(documented) {
		if settable[n] != "" || exempt[n] != "" {
			continue
		}
		undeliverable = append(undeliverable, site{n, documented[n]})
	}
	for _, n := range sortedKeys(settable) {
		if _, ok := documented[n]; ok {
			continue
		}
		undocumented = append(undocumented, site{n, settable[n]})
	}

	// A row that is no longer needed. The same half figurecheck and varcheck
	// have, and the reason they have it: staleness that is not reported is an
	// allowance that outlives its argument.
	var stale []string
	for _, n := range order {
		switch {
		case documented[n] == "":
			stale = append(stale, fmt.Sprintf(
				"%s is exempt and %s no longer documents it, so the row can go", n, referencePath))
		case settable[n] != "":
			stale = append(stale, fmt.Sprintf(
				"%s is exempt and the module now sets it at %s, so the row can go", n, settable[n]))
		}
	}

	if len(undeliverable) == 0 && len(undocumented) == 0 && len(stale) == 0 {
		fmt.Printf("wirecheck: %d variables documented, %d the module can set, %d exempt with a reason\n",
			len(documented), countReachable(documented, settable), len(order))
		return
	}

	for _, f := range undeliverable {
		fmt.Fprintf(os.Stderr,
			"  %s is documented at %s and no supported deploy can set it\n", f.name, f.where)
	}
	for _, f := range undocumented {
		fmt.Fprintf(os.Stderr,
			"  %s is set at %s and %s does not document it\n", f.name, f.where, referencePath)
	}
	for _, s := range stale {
		fmt.Fprintf(os.Stderr, "  %s\n", s)
	}
	fmt.Fprintf(os.Stderr,
		"\nwirecheck: %d documented with no way to set them, %d set and undocumented, %d stale exemptions.\n",
		len(undeliverable), len(undocumented), len(stale))
	fmt.Fprintf(os.Stderr,
		"Add an env block in %s, or a row to %s saying why it cannot be set there.\n",
		modulePath, exemptionsPath)
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
func floors(documented, settable map[string]string) error {
	if len(documented) < minDocumented {
		return fmt.Errorf(
			"read only %d variables out of %s, which is below the floor of %d. "+
				"The tables are probably not the shape this parses; a check that "+
				"cannot find anything looks exactly like a page with nothing in it",
			len(documented), referencePath, minDocumented)
	}
	if len(settable) < minSettable {
		return fmt.Errorf(
			"found only %d environment variables in %s, which is below the floor of %d. "+
				"The env blocks are probably not the shape this parses",
			len(settable), modulePath, minSettable)
	}
	return nil
}

// exemptions returns the reason per variable, and the file's order so that a
// stale row is reported where a reader will find it.
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
		if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
			return nil, nil, fmt.Errorf("%s:%d has no reason. Two tab separated fields, "+
				"the variable and why the module cannot set it", exemptionsPath, i+1)
		}
		name := strings.TrimSpace(parts[0])
		if _, dup := reasons[name]; dup {
			return nil, nil, fmt.Errorf("%s:%d names %s twice", exemptionsPath, i+1, name)
		}
		reasons[name] = strings.TrimSpace(parts[1])
		order = append(order, name)
	}
	return reasons, order, nil
}
