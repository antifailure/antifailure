// Command contactcheck refuses a contact route this project cannot answer.
//
// THE DEFECT THIS EXISTS FOR. CODE_OF_CONDUCT.md named conduct@antifailure.dev
// as the place a person reporting harassment should go. The legal pages named
// security@antifailure.dev for a security researcher. Neither address has ever
// been able to receive anything, and the reason is not subtle:
//
//	$ dig +short MX antifailure.dev                        (empty)
//	$ dig +short TXT antifailure.dev                        "v=spf1 -all"
//	$ dig +short TXT _dmarc.antifailure.dev                 "v=DMARC1; p=reject; ..."
//	$ dig +short TXT resend._domainkey.antifailure.dev      "v=DKIM1; p="
//
// No mail exchanger, so nothing can be delivered. An SPF policy authorising no
// sender and a DMARC policy of reject, so nothing can be sent either. A DKIM
// record with an empty p=, which is how RFC 6376 spells a REVOKED key. The
// domain is configured to send nothing and receive nothing, and every address
// on it is silence.
//
// WHY THAT IS WORSE THAN PUBLISHING NOTHING. A published address is a promise
// that somebody is on the other end. A researcher sitting on a finding, a
// person asking to have their data deleted, and somebody reporting harassment
// all send into the same void, and none of them can tell the difference
// between a mailbox nobody reads and a mailbox that does not exist. They wait
// instead of looking for the route that would have worked. Saying nothing at
// least sends them looking.
//
// The site had already been fixed and the repository had not, which is the
// shape of most of these: two things that should have moved together and only
// one did. www/components/pages/company/Contact.tsx carried a callout headed
// "Email is not a contact route" while CODE_OF_CONDUCT.md, in the same commit,
// named a mailbox. web/apps/api/test/legal-facts.test.ts guards the two site
// pages. Nothing guarded the repository, so this does.
//
// WHAT IT ASSERTS, and this is a choice with a cost either way.
//
// It does NOT resolve MX at check time. Doing so would make a gate on every
// pull request depend on the network and on somebody else's resolver, which
// buys a fresher answer at the price of a red build whenever DNS is slow and a
// green one whenever a resolver lies. It is also the wrong question: an MX
// record proves a server accepts mail, not that a person reads what lands
// there, and the failure being guarded against is nobody reading it.
//
// Instead it inverts the default. Every address published in this repository
// must be accounted for in tools/docs/contact-routes.tsv, and an address
// nobody has written a row for FAILS. A static denylist of known-dead
// addresses cannot notice a new domain; a default of no can, because a new
// domain arrives with no row.
//
// Two things are exempt from needing a row, and both are exempt because they
// can never be a route at all rather than because somebody vouched for them:
//
//   - The domains RFC 2606 and RFC 6761 reserve for documentation:
//     example.com, example.net, example.org, and anything under the .test,
//     .example, .invalid and .localhost top level domains. These exist so that
//     an illustration cannot accidentally name a real mailbox.
//   - Addresses under users.noreply.github.com, which is GitHub's own
//     no-reply space and reads as one to anybody who sees it in a git log.
//
// THE THREE VERDICTS a row may carry are a closed set, because "allowed" with
// a free text reason is a box anybody can tick.
//
//	receives           a person reads mail sent here, and the reason says who.
//	                   REFUSED outright when the domain is one of the dead
//	                   domains below. This is the rule that cannot be argued
//	                   with: no wording makes antifailure.dev receive mail.
//
//	not-a-route        the string is a value the software writes, a git
//	                   identity, or an illustration in a placeholder. Nobody is
//	                   being invited to send anything to it.
//
//	records-a-defect   the address is quoted inside a description of a mailbox
//	                   that did not work, which is what this very file does.
//	                   REFUSED unless the same file also states that the
//	                   address cannot receive, so the quotation cannot be the
//	                   only thing a reader takes away.
//
// One rule sits above the verdicts and no row can excuse it: AT A DOMAIN
// PROVEN DEAD, no text in this repository may read as an instruction to write
// there. The words just before the address are checked rather than the words
// on the same line, because the sentence that started this wrapped, with the
// verb at the end of one line and the address at the start of the next.
//
// That rule is what makes a row per file safe. A row is matched by file and
// address, so one row covers every occurrence in that file, and a file
// legitimately quoting a dead address once would otherwise license a genuine
// instruction three paragraphs down. It also costs something worth naming: a
// sentence about the history has to say that a file named an address rather
// than that it sent people to one. Four places in this repository were
// reworded for it, this file included.
//
// The rule is scoped to dead domains rather than applied to every address,
// because applied everywhere it convicted five illustrations that are fine: a
// sign-in field whose label is the word Email, and a film showing an invented
// customer row being masked whose sample text is itself an instruction to
// email somebody. None of those is a promise this project has to keep. A
// domain nobody has checked is caught by the missing row instead.
//
// A row matching nothing is reported, so the list cannot rot.
//
// WHAT THIS CANNOT CATCH, said here rather than left for somebody to discover.
//
//  1. It cannot tell whether a domain that is not in deadDomains receives.
//     If this project acquires a second domain and publishes a mailbox on it
//     with a row saying `receives`, the gate passes and the mail may still go
//     nowhere. Adding a domain to deadDomains is a human check with `dig`.
//  2. `records-a-defect` is satisfied by evidence anywhere in the same file.
//     Somebody could write a genuine instruction to a dead address in a file
//     that elsewhere explains the address is dead, and this would pass. That
//     is a strange thing to write and it is visible in a diff, which is the
//     whole of the defence.
//  3. It reads source, not the rendered page. An address assembled at runtime
//     from parts, or read from an environment variable, is invisible to it.
//  4. It says nothing about whether a route that is not an address works. A
//     dead GitHub link is claimcheck's and links' business, not this one's.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const routesPath = "tools/docs/contact-routes.tsv"

// address matches an email address as a reader would recognise one. Kept
// deliberately plain: the aim is to notice a string that looks like somewhere
// to write, not to implement RFC 5322.
var address = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)

// reservedTLDs and reservedDomains are the names RFC 2606 and RFC 6761 set
// aside so that documentation cannot name a real mailbox by accident. An
// address under one of these is an illustration by construction and needs no
// row: there is nothing on the other end and there never can be.
var (
	reservedTLDs    = map[string]bool{"test": true, "example": true, "invalid": true, "localhost": true}
	reservedDomains = map[string]bool{"example.com": true, "example.net": true, "example.org": true}
)

// noReplySuffix is GitHub's no-reply space. A commit author at
// users.noreply.github.com is the convention for an automated identity and
// reads as one, so it is not a mailbox anybody mistakes for a route.
const noReplySuffix = "users.noreply.github.com"

// deadDomains are the domains proven not to receive, with the evidence and the
// date somebody looked. A `receives` row at one of these is refused no matter
// what its reason says.
//
// This is a list of PROVEN failures rather than the gate's main mechanism. The
// main mechanism is that an address with no row at all fails, which is what
// covers a domain nobody has checked yet. This list exists so that the one
// domain we have checked cannot be re-argued in a reason column.
var deadDomains = map[string]string{
	"antifailure.dev": "no MX record at all, SPF `v=spf1 -all`, DMARC `p=reject` with strict alignment, " +
		"and a DKIM record at resend._domainkey whose empty p= revokes the key (RFC 6376). " +
		"Resolved against three independent resolvers on 2026-09-02, which agreed",
}

// verdicts are the only words a row may carry. A closed set, because the
// failure being guarded against is somebody writing a plausible sentence in a
// free text column and moving on.
const (
	verdictReceives = "receives"
	verdictNotRoute = "not-a-route"
	verdictDefect   = "records-a-defect"
)

// invitation is the language that turns a string into a promise. It is matched
// against the text immediately BEFORE an address, because that is where the
// instruction sits. The sentence that started this wrapped, with the verb at
// the end of one line and the address at the start of the next, so the scan is
// over a window of the file rather than over one line.
//
// It is broad on purpose, and it can afford to be, because it only ever runs
// against an address at a domain already proven dead. A false positive there
// costs somebody one sentence reworded. The same breadth applied everywhere
// convicted a placeholder in a sign-in field.
//
// The bare word `contact` was in this list and is not any more. It is a noun
// here as often as a verb, and it convicted this file's own opening line,
// which says that contactcheck refuses a contact route. So the verb forms are
// spelled out and the noun is left alone. `written by` and `writing` are out
// for the same reason: api/README.md says a probe row "is written by
// .github/workflows/waitlist.yml", which is a statement about a script.
var invitation = regexp.MustCompile(`(?i)\b(?:mailbox|mail\s+(?:to|us|me)|writ(?:e|ing|ten)\s+to|` +
	`report(?:s|ed|ing)?\s+(?:\w+\s+){0,2}(?:to|at)\b|contact(?:ed)?\s+(?:us|me|them|at|the\s+\w+)|` +
	`reach(?:ed)?\s+(?:us|me|out|at)|send\s+(?:it\s+)?to|sent\s+to|complaints?\s+to|` +
	`questions?\s+to|get\s+in\s+touch)\b`)

// invitationAdjacent is the shorter half of the same rule: a single word that
// only means an invitation when it sits immediately in front of the address.
//
// `email` cannot go in the list above. It appears in every sentence ABOUT a
// mailbox as well as in every instruction to use one, and the changelog
// fragment that records this whole defect opens "an email address that cannot
// receive mail", which is the opposite of an invitation and would have been
// convicted by it. Anchored to the end of the window it means what it says:
// "email security@...", "Email: security@...", "email us at security@...".
var invitationAdjacent = regexp.MustCompile(`(?i)\b(?:e-?mail|mail|address)\b[\s:,\-]*(?:us\s*)?(?:at\s*)?$`)

// invitationWindow is how far back from an address the invitation language is
// looked for, in bytes of whitespace normalised text. Two wrapped lines of
// prose. Long enough to span the line break that hid the original defect,
// short enough that an unrelated sentence earlier in a paragraph does not
// convict the address.
const invitationWindow = 120

// deadEvidence is what a file must say for a `records-a-defect` row to stand:
// somewhere in the same file, in the reader's sight, the address is described
// as one that does not work.
var deadEvidence = regexp.MustCompile(`(?i)cannot receive|can never receive|could not receive|does not receive|` +
	`never been able to receive|no mail exchanger|delivered nowhere|do not email|is not a contact route|` +
	`bounces|authorises no sender|authorizes no sender`)

// skipDirs are trees whose contents are not ours to publish or not read by
// anybody. `docs/plan` is NOT here: it is a working note rather than a
// published page, but it is in the repository and a reader can open it.
var skipDirs = map[string]bool{
	"node_modules": true, "vendor": true, "dist": true, "out": true,
	".git": true, "testdata": true, "__pycache__": true,
}

// skipFiles are the paths whose addresses are already checked somewhere else.
//
// CHANGELOG.md is assembled by `just changelog` out of the `.changes`
// fragments, every one of which is scanned here. Requiring a second row for
// the generated copy would mean the gate went red between a fragment landing
// and the changelog being rebuilt, which is a red build for a file nobody
// edited. Its source is covered, so it is not.
//
// The routes file itself is here for the obvious reason: it is a list of
// addresses, so scanning it would demand a row for every row, and then a row
// for that.
var skipFiles = map[string]bool{"CHANGELOG.md": true, routesPath: true}

// testFile reports whether a path is a test, a fixture, or a lockfile.
//
// A fixture is not published. Every SAML assertion, every seeded customer and
// every masking corpus in this repository carries addresses, all of them at
// reserved domains already, and scanning them would bury the twenty places
// that are real under two hundred that are not.
func testFile(rel string) bool {
	base := filepath.Base(rel)
	switch {
	case strings.HasSuffix(base, "_test.go"),
		strings.Contains(base, ".test."),
		strings.Contains(base, ".spec."),
		base == "go.sum", base == "package-lock.json", base == "pnpm-lock.yaml":
		return true
	}
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if seg == "test" || seg == "tests" || seg == "__tests__" {
			return true
		}
	}
	return false
}

// row is one line of the routes file.
type row struct {
	path, address, verdict, why string
	line                        int
	used                        bool
}

// finding is one thing wrong, in the words a person reading a red build needs.
type finding struct {
	path    string
	num     int
	address string
	problem string
	fix     string
}

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	if args := flag.Args(); len(args) > 0 {
		*root = args[0]
	}
	if err := run(*root, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "\ncontactcheck: %v\n", err)
		os.Exit(1)
	}
}

func run(root string, out io.Writer) error {
	rows, err := loadRows(filepath.Join(root, routesPath))
	if err != nil {
		return err
	}

	files, err := collect(root)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("found no files under %s, so this check is looking in the wrong place", root)
	}

	var found []finding
	scanned := 0
	for _, rel := range files {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return err
		}
		scanned++
		found = append(found, Check(rel, string(body), rows)...)
	}

	// A row that matches nothing is a finding of its own. Without this the file
	// accumulates excuses for addresses that were deleted years ago, and the
	// next person reads it as a description of the repository rather than as a
	// list somebody stopped maintaining.
	for _, r := range rows {
		if !r.used {
			found = append(found, finding{
				path: routesPath, num: r.line, address: r.address,
				problem: fmt.Sprintf("this row claims %s appears in %s, and it does not", r.address, r.path),
				fix:     "delete the row, or correct the path if the address moved",
			})
		}
	}

	sort.Slice(found, func(i, j int) bool {
		if found[i].path != found[j].path {
			return found[i].path < found[j].path
		}
		return found[i].num < found[j].num
	})

	// Write errors are ignored explicitly, once, with a reason: the verdict of
	// this tool is its exit code, not its report, so a broken pipe on stdout
	// changes what a person can read and not whether the build should fail.
	report := func(format string, args ...any) { _, _ = fmt.Fprintf(out, format, args...) }

	for _, f := range found {
		report("%s:%d  %s\n    %s.\n    %s.\n", f.path, f.num, f.address, f.problem, f.fix)
	}
	report("contactcheck: %d files, %d %s in %s, %d %s a route this project cannot answer\n",
		scanned, len(rows), plural(len(rows), "row", "rows"), routesPath,
		len(found), plural(len(found), "place publishes", "places publish"))

	if len(found) > 0 {
		return fmt.Errorf("%d %s a contact route that does not work",
			len(found), plural(len(found), "place publishes", "places publish"))
	}
	return nil
}

// Check reports every address in one file that is not accounted for.
//
// Exported so a test can drive it without a tree, and so the rules stay in one
// place rather than being restated by whatever reads them next.
func Check(rel, body string, rows []*row) []finding {
	var out []finding
	evidence := deadEvidence.MatchString(flow(body))

	for _, hit := range address.FindAllStringIndex(body, -1) {
		found := body[hit[0]:hit[1]]
		domain := domainOf(found)
		if exempt(domain) {
			continue
		}
		num := 1 + strings.Count(body[:hit[0]], "\n")

		r := match(rows, rel, found)
		if r == nil {
			out = append(out, finding{
				path: rel, num: num, address: found,
				problem: "no row in " + routesPath + " accounts for this address, so nothing says whether anybody reads it",
				fix: "route the reader somewhere that resolves, or add a row saying which of " +
					verdictReceives + ", " + verdictNotRoute + " or " + verdictDefect + " this is and why",
			})
			continue
		}
		r.used = true

		if why, dead := deadDomains[domain]; dead && r.verdict == verdictReceives {
			out = append(out, finding{
				path: rel, num: num, address: found,
				problem: "the row says " + verdictReceives + ", and " + domain + " cannot receive mail: " + why,
				fix:     "publish a route that resolves instead, and say plainly in the file that there is no mailbox",
			})
			continue
		}

		window := before(body, hit[0])
		if _, dead := deadDomains[domain]; dead &&
			(invitation.MatchString(window) || invitationAdjacent.MatchString(window)) {
			out = append(out, finding{
				path: rel, num: num, address: found,
				problem: "the sentence in front of this reads as an instruction to write here, and " + domain +
					" cannot receive mail",
				fix: "point the reader at a route that resolves; if you are describing the address rather than " +
					"offering it, say `named X` rather than `write to X`",
			})
			continue
		}

		if r.verdict == verdictDefect && !evidence {
			out = append(out, finding{
				path: rel, num: num, address: found,
				problem: "the row says " + verdictDefect + ", and nothing in this file tells the reader the address does not work",
				fix:     "say in the file that the address cannot receive mail, or the quotation reads as an instruction",
			})
			continue
		}
	}
	return out
}

// wrapping is what separates two words of a phrase when prose reaches the
// right margin: a line break, and then the comment marker that opens the next
// line. `no mail exchanger` is three words in the source and was three words
// on two lines in ci.yml, which is exactly the sentence deadEvidence is
// looking for and could not see.
var wrapping = regexp.MustCompile(`[\s#*]+|//+`)

// flow puts a wrapped sentence back on one line so a phrase can be matched
// across the break. Only used for the file wide evidence test, where the
// question is whether the file says something at all rather than where.
func flow(body string) string {
	return wrapping.ReplaceAllString(body, " ")
}

// before returns the whitespace normalised text immediately preceding an
// address, which is where an instruction to use it sits.
func before(body string, at int) string {
	start := at - invitationWindow*2
	if start < 0 {
		start = 0
	}
	window := strings.Join(strings.Fields(body[start:at]), " ")
	if len(window) > invitationWindow {
		window = window[len(window)-invitationWindow:]
	}
	return window
}

// domainOf returns the lowercased domain of an address, without a trailing dot
// picked up from the end of a sentence.
func domainOf(addr string) string {
	at := strings.LastIndex(addr, "@")
	return strings.ToLower(strings.Trim(addr[at+1:], "."))
}

// exempt reports whether a domain can never be a route at all.
func exempt(domain string) bool {
	if reservedDomains[domain] || domain == noReplySuffix || strings.HasSuffix(domain, "."+noReplySuffix) {
		return true
	}
	parts := strings.Split(domain, ".")
	return reservedTLDs[parts[len(parts)-1]]
}

// match finds the row for one address in one file.
func match(rows []*row, rel, addr string) *row {
	for _, r := range rows {
		if r.path == rel && strings.EqualFold(r.address, addr) {
			return r
		}
	}
	return nil
}

// loadRows reads the routes file. Four tab separated fields, comments and
// blank lines skipped.
func loadRows(path string) ([]*row, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%s is the whole point of this check: %w", routesPath, err)
	}
	defer func() { _ = f.Close() }()

	var rows []*row
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for n := 1; s.Scan(); n++ {
		line := s.Text()
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 4 {
			return nil, fmt.Errorf("%s:%d has %d tab separated fields, wanted 4: path, address, verdict, why",
				routesPath, n, len(parts))
		}
		r := &row{
			path: strings.TrimSpace(parts[0]), address: strings.TrimSpace(parts[1]),
			verdict: strings.TrimSpace(parts[2]), why: strings.TrimSpace(parts[3]), line: n,
		}
		switch r.verdict {
		case verdictReceives, verdictNotRoute, verdictDefect:
		default:
			return nil, fmt.Errorf("%s:%d has verdict %q, which is not one of %s, %s, %s",
				routesPath, n, r.verdict, verdictReceives, verdictNotRoute, verdictDefect)
		}
		if r.why == "" {
			return nil, fmt.Errorf("%s:%d has no reason, and a row with no argument behind it is somebody "+
				"silencing a finding they did not understand", routesPath, n)
		}
		rows = append(rows, r)
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("%s has no rows, so this check would pass a repository full of dead mailboxes", routesPath)
	}
	return rows, nil
}

// collect walks the repository and returns every text file worth scanning.
func collect(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (skipDirs[name] || (strings.HasPrefix(name, ".") && name != ".github" && name != ".changes" && name != ".githooks")) {
				return fs.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if skipFiles[rel] || testFile(rel) || !text(path) {
			return nil
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// textExts are the extensions a published address can live in. An allowlist
// rather than a size or content sniff, because the alternative is reading
// every binary in the tree to find out it is a binary.
var textExts = map[string]bool{
	".md": true, ".mdx": true, ".txt": true, ".go": true, ".ts": true, ".tsx": true,
	".js": true, ".jsx": true, ".mjs": true, ".cjs": true, ".json": true, ".yml": true,
	".yaml": true, ".toml": true, ".sh": true, ".bash": true, ".sql": true, ".html": true,
	".css": true, ".tf": true, ".tfvars": true, ".bicep": true, ".py": true, ".rb": true,
}

// textNames are the extensionless files that still carry prose or configuration.
var textNames = map[string]bool{
	"justfile": true, "Justfile": true, "Dockerfile": true, "Makefile": true,
	"LICENSE": true, "NOTICE": true, "pre-commit": true, "pre-push": true, "commit-msg": true,
}

func text(path string) bool {
	base := filepath.Base(path)
	if textNames[base] {
		return true
	}
	if strings.HasPrefix(base, "Dockerfile") {
		return true
	}
	return textExts[filepath.Ext(base)]
}

// plural picks the word for a count, so a failing run does not report
// "1 places". The message is the only thing a person reads when this gate goes
// red.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
