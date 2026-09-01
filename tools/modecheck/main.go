// Command modecheck fails the build when prose enumerates the egress modes
// wrongly.
//
// The egress modes are the one part of this product whose complete list is
// declared in a machine readable file, so a sentence naming them is a claim
// that can be checked rather than reviewed. It had drifted anyway. The product
// page's own title read "Simulate, capture, mock, or deny" against a schema
// whose modes are block, allow, capture, mock, sandbox and synth: two of the
// four words were not modes at all, and four real modes were missing. The
// README, llms.txt, the product FAQ and a published blog post all separately
// claimed there were five, because synth was added to the schema and to the
// proxy and to nothing that describes them.
//
// The modes are read from schemas/manifest.v1.json rather than written down
// here, because a checker carrying its own copy of the list is one more place
// for the list to be wrong.
//
// WHAT THIS DELIBERATELY DOES NOT CHECK, because a gate that cries wolf gets
// deleted. It does not flag a sentence that names some modes without claiming
// to name all of them: "sandbox, capture, mock and synth all terminate TLS" is
// true and useful, and the quickstart's explicitly hedged "the short version"
// is fine. Only three shapes are checked, and each is one somebody can only
// have meant as a complete claim:
//
//  1. A stated count. "five modes" in a sentence about egress is checkable
//     against the enum's length, and was wrong in five places.
//  2. A name that is not a mode, used as a peer of names that are. "Simulate,
//     capture, mock, or deny" and a rule label reading "*:deny" both name
//     behaviours the manifest would refuse. Two places.
//  3. Mode names following a phrase that promises the whole set, such as "each
//     host gets a mode:". Five names after that phrase is a false claim; five
//     names after nothing is a description. Three places.
//
// table.go adds a fourth, on a different surface and for every closed set the
// schema declares rather than only the modes: a reference table cell that
// claims to be listing a key's allowed values must list all of them. Note the
// narrow promise, which the tool's own output repeats. It does not check every
// closed set the schema declares, because the values are ordinary English and
// scanning for them is not reliable. It checks the ones a document claims to
// be listing. table.go's own comment carries the measurements behind that.
package main

import (
	"encoding/json"
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

// trees are the places prose about the modes lives. The engine is not here:
// Go code names the modes as constants, and a constant cannot be wrong about
// itself.
var trees = []string{"www", "console", "docs/src/content/docs", "README.md"}

// notModes are words used as though they were modes and are not. The list is
// closed and short on purpose: every entry is a word this repository or its
// sibling documents has actually reached for in place of a real mode name, so
// each one is a mistake somebody has already made rather than a word that
// might one day be wrong.
//
// "deny" and "record" are the dangerous pair, because both are ordinary
// English that appears correctly all over these documents ("unknown
// destinations are denied"). That is why rule 2 only fires when the word sits
// inside a list of real mode names, where it is being used as a peer.
var notModes = map[string]string{
	"simulate":    "mock",
	"simulated":   "mock",
	"deny":        "block",
	"denied":      "block",
	"record":      "capture",
	"recorded":    "capture",
	"intercept":   "capture",
	"stub":        "mock",
	"fake":        "mock",
	"passthrough": "allow",
	"forward":     "allow",
}

// exhaustive are the phrases that turn a list of names into a complete claim.
// Each is anchored on a colon or on the word "of", so that "the modes are
// covered in [egress]" is not read as the start of an enumeration.
var exhaustive = regexp.MustCompile(`(?i)\b(?:` +
	`gets? (?:a|one) mode:` +
	`|gets? one of (?:the |[a-z]+ )?modes?:?` +
	`|(?:per-host|per host) decision:` +
	`|the (?:[a-z-]+ )?modes are\s+(?:[` + "`" + `*_]*[a-z])` +
	`|(?:one|1) of (?:the |[a-z]+ )?modes?:?` +
	`)`)

// counted matches a stated number of modes. Counts below two are skipped: "each
// host gets one mode" says how many modes a host gets, not how many exist, and
// it is true.
var counted = regexp.MustCompile(`(?i)\b(` + strings.Join(numberWords, "|") + `|\d+)\s+((?:[a-z-]+\s+){0,3})modes?\b`)

var numberWords = []string{
	"two", "three", "four", "five", "six", "seven", "eight", "nine", "ten",
	"eleven", "twelve",
}

var numberValue = map[string]int{
	"two": 2, "three": 3, "four": 4, "five": 5, "six": 6,
	"seven": 7, "eight": 8, "nine": 9, "ten": 10, "eleven": 11, "twelve": 12,
}

// egressish is what makes a count about these modes rather than some other
// enum. `github.mode` has three values and the docs say "Two modes" about it;
// a job in the Azure guide has three. Neither mentions a host, egress, or any
// mode name, and neither should fail this check.
var egressish = regexp.MustCompile(`(?i)\b(egress|per-host|per host|hosts?|outbound)\b`)

// failureMode excludes the unrelated idiom. "Two failure modes to watch for"
// is not a claim about this enum.
var failureMode = regexp.MustCompile(`(?i)\b(failure|fail)\s+modes?\b`)

// label matches a rule label of the shape a manifest line has, which is how the
// homepage film writes its five example rules. The film had "*:deny" in it, and
// the manifest validator refuses a "*" rule in any mode but block, so the one
// label a reader was most likely to copy was the one that could not work.
var label = regexp.MustCompile(`"([a-z0-9*][a-z0-9.*_-]*):([a-z]+)"`)

var fence = regexp.MustCompile("^\\s*```")

type finding struct {
	file, why string
	line      int
	text      string
}

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	if args := flag.Args(); len(args) > 0 {
		*root = args[0]
	}
	if err := run(*root, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "\nmodecheck: %v\n", err)
		os.Exit(1)
	}
}

func run(root string, out io.Writer) error {
	modes, err := Modes(filepath.Join(root, "schemas", "manifest.v1.json"))
	if err != nil {
		return err
	}
	if len(modes) < 2 {
		return fmt.Errorf("read %d modes from the schema, so this check is reading the wrong field", len(modes))
	}

	files, err := prose(root)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("found no prose under %s, so this check is looking in the wrong place", root)
	}

	tree, err := loadSchema(filepath.Join(root, "schemas", "manifest.v1.json"))
	if err != nil {
		return err
	}

	var found []finding
	tables := 0
	for _, f := range files {
		body, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			return err
		}
		found = append(found, Check(f, string(body), modes)...)

		if strings.HasSuffix(f, ".md") {
			tables++
			found = append(found, CheckTable(f, string(body), tree)...)
		}
	}

	sort.Slice(found, func(i, j int) bool {
		if found[i].file != found[j].file {
			return found[i].file < found[j].file
		}
		return found[i].line < found[j].line
	})
	report := func(format string, args ...any) { _, _ = fmt.Fprintf(out, format, args...) }
	for _, f := range found {
		report("%s:%d  %s\n    %s\n", f.file, f.line, f.why, f.text)
	}
	// The second line states the narrower promise on purpose. This does not
	// check every closed set the schema declares, which is not reliably
	// possible in prose; it checks the ones a document claims to be listing.
	report("modecheck: %d files scanned for prose about the egress modes (%s)\n",
		len(files), strings.Join(modes, ", "))
	report("modecheck: %d markdown files scanned for reference table cells that "+
		"claim to list a closed set the schema declares\n", tables)
	report("modecheck: %d false enumerations\n", len(found))

	if len(found) > 0 {
		return fmt.Errorf("%d places describe a closed set the schema declares as something it is not", len(found))
	}
	return nil
}

// Modes reads the egress mode enum out of the manifest schema.
//
// It looks for the mode property under the egress rule definition rather than
// for any property named "mode", because the schema has a second unrelated
// "mode" under github whose values are actions, app and off. Matching that one
// would make this checker enforce the wrong list, which is the exact failure
// it exists to prevent.
func Modes(schemaPath string) ([]string, error) {
	body, err := os.ReadFile(schemaPath)
	if err != nil {
		return nil, err
	}
	var doc any
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", schemaPath, err)
	}

	// Every matching enum is collected and they are then required to agree,
	// rather than the longest one winning. Taking the longest would mean the
	// block and capture test below decided nothing: the egress enum has six
	// values and github's has three, so it would win either way, and a
	// seventh value added to some unrelated mode would silently take over the
	// list this whole tool enforces.
	var found [][]string
	var walk func(node any)
	walk = func(node any) {
		switch n := node.(type) {
		case []any:
			for _, v := range n {
				walk(v)
			}
		case map[string]any:
			if raw, has := n["mode"]; has {
				if vals, ok := enum(raw); ok && contains(vals, "block") && contains(vals, "capture") {
					found = append(found, vals)
				}
			}
			for _, v := range n {
				walk(v)
			}
		}
	}
	walk(doc)

	if len(found) == 0 {
		return nil, fmt.Errorf("%s: found no mode enum containing block and capture", schemaPath)
	}
	first := strings.Join(found[0], ",")
	for _, f := range found[1:] {
		if strings.Join(f, ",") != first {
			return nil, fmt.Errorf(
				"%s: two different mode enums contain block and capture (%s, and %s). "+
					"This tool cannot tell which one the prose is describing",
				schemaPath, first, strings.Join(f, ","))
		}
	}
	return found[0], nil
}

func enum(node any) ([]string, bool) {
	m, ok := node.(map[string]any)
	if !ok {
		return nil, false
	}
	raw, ok := m["enum"].([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

// Check reports the false enumerations in one document.
//
// The text is flattened to one line before the rules run, because the claims
// this is looking for cross line breaks: the README's mode list is five lines
// of wrapped markdown and a JSX paragraph is however many lines the formatter
// chose. A line based check would see five fragments and no claim in any of
// them. Offsets are mapped back to line numbers so the report still points
// somewhere a person can open.
func Check(name, body string, modes []string) []finding {
	flat, lineAt := flatten(name, body)
	real := map[string]bool{}
	for _, m := range modes {
		real[m] = true
	}

	var out []finding
	seen := map[int]bool{}
	add := func(offset int, why, _ string) {
		line := lineAt(offset)
		// One finding per line. A sentence can trip two rules at once and
		// reporting it twice teaches nobody anything the first line did not.
		if seen[line] {
			return
		}
		seen[line] = true
		out = append(out, finding{file: name, line: line, why: why, text: excerpt(flat, offset)})
	}

	for _, s := range sentences(flat) {
		checkCount(s, modes, real, add)
		checkPeers(s, real, add)
		checkPromise(s, real, add)
	}
	checkLabels(flat, real, add)
	return out
}

type sentence struct {
	text  string
	start int
}

// checkCount is rule 1: a stated number of egress modes must be the real one.
func checkCount(s sentence, modes []string, real map[string]bool, add func(int, string, string)) {
	if failureMode.MatchString(s.text) {
		return
	}
	for _, m := range counted.FindAllStringSubmatchIndex(s.text, -1) {
		word := strings.ToLower(s.text[m[2]:m[3]])
		between := s.text[m[4]:m[5]]
		n, ok := numberValue[word]
		if !ok {
			// A digit. Anything this project would write as a numeral here is
			// a count, so parse it and fall through to the same comparison.
			if _, err := fmt.Sscanf(word, "%d", &n); err != nil {
				continue
			}
		}
		if n < 2 || n == len(modes) {
			continue
		}
		// "five failure modes" is caught above; this catches the words in
		// between, so that "five deployment modes" is not read as a claim
		// about egress.
		if strings.Contains(strings.ToLower(between), "failure") {
			continue
		}
		if !egressish.MatchString(s.text) && !namesAny(s.text, real) {
			continue
		}
		add(s.start+m[0], fmt.Sprintf(
			"says there are %d egress modes. There are %d: %s. Say %d, or say which ones without a count.",
			n, len(modes), strings.Join(modes, ", "), len(modes)), s.text)
	}
}

// checkPeers is rule 2: a word that is not a mode, standing in a list beside
// words that are.
//
// It reads only genuine lists, which is the whole reason it can be trusted.
// An earlier version treated any two mode-ish words in a row as a list and
// flagged a UI caption reading "capture recorded, never posted" and a status
// pill reading `tone="block">denied`. Neither enumerates anything. A list has
// commas or the word or in it, so that is what runs now requires.
func checkPeers(s sentence, real map[string]bool, add func(int, string, string)) {
	for _, run := range runs(s.text, real) {
		var bad []string
		nReal := 0
		for _, w := range run.words {
			if real[strings.ToLower(w)] {
				nReal++
				continue
			}
			if right, ok := notModes[strings.ToLower(w)]; ok {
				bad = append(bad, w+" (mean "+right+")")
			}
		}
		// One real mode name is enough context: a comma separated list holding
		// a mode and a word that is not one is somebody enumerating modes and
		// getting one wrong.
		if len(bad) > 0 && nReal >= 1 {
			add(s.start+run.start, "names "+strings.Join(bad, " and ")+
				" as though it were a mode. It is not one, and the manifest would refuse it.",
				s.text)
			return
		}
	}
}

// window is how far past an exhaustive phrase rule 3 looks for names. The
// longest real one of these, the README's, runs to about 340 characters.
const window = 500

// checkPromise is rule 3: a phrase that promises the whole set, followed by
// part of it.
//
// The names are counted across the text after the phrase rather than inside
// one unbroken list, because the honest form of this sentence interleaves each
// name with what it does: "BLOCK refuses, ALLOW passes with a rate limit,
// SANDBOX swaps in test credentials". Counting only unbroken runs read that as
// three separate lists of one and called a correct sentence wrong.
func checkPromise(s sentence, real map[string]bool, add func(int, string, string)) {
	m := exhaustive.FindStringIndex(s.text)
	if m == nil {
		return
	}
	rest := s.text[m[1]:]
	if len(rest) > window {
		rest = rest[:window]
	}

	named := map[string]bool{}
	for _, w := range word.FindAllString(rest, -1) {
		if w = strings.ToLower(w); real[w] {
			named[w] = true
		}
	}
	// Below three names it is not an enumeration, it is a sentence that
	// happens to mention a mode after a colon.
	if len(named) < 3 || len(named) >= len(real) {
		return
	}

	var missing []string
	for m := range real {
		if !named[m] {
			missing = append(missing, m)
		}
	}
	sort.Strings(missing)
	add(s.start+m[0], fmt.Sprintf(
		"promises the whole set and then names %d of %d modes. Missing: %s.",
		len(named), len(real), strings.Join(missing, ", ")), s.text)
}

// checkLabels is the second half of rule 2: a manifest shaped label in a code
// sample or a diagram, whose mode half is not a mode.
func checkLabels(flat string, real map[string]bool, add func(int, string, string)) {
	for _, m := range label.FindAllStringSubmatchIndex(flat, -1) {
		mode := flat[m[4]:m[5]]
		if real[mode] {
			continue
		}
		right, ok := notModes[mode]
		if !ok {
			continue
		}
		add(m[0], "labels a rule "+flat[m[2]:m[3]]+":"+mode+", and "+mode+
			" is not a mode. Write "+right+".", flat[m[0]:m[1]])
	}
}

type modeRun struct {
	words []string
	start int
}

var word = regexp.MustCompile(`[A-Za-z]+`)

// runs finds maximal comma or "or" separated sequences of list items that are
// all mode names or near misses for one. Any other word breaks the sequence,
// and so does the absence of a separator: two mode-ish words merely adjacent
// are prose, not a list. "Simulate, capture, mock, or deny" is one run of
// four; "capture recorded, never posted" is two runs of one and is dropped.
func runs(text string, real map[string]bool) []modeRun {
	var out []modeRun
	var cur modeRun
	inRun := false
	lastEnd := 0
	joined := false

	flush := func() {
		if inRun && len(cur.words) >= 2 {
			out = append(out, cur)
		}
		inRun = false
		cur = modeRun{}
	}

	for _, m := range word.FindAllStringIndex(text, -1) {
		w := strings.ToLower(text[m[0]:m[1]])
		switch {
		case real[w] || notModes[w] != "":
			// A separator has to sit between two items for them to be a list.
			// Either a comma somewhere in the gap, or the word or or and,
			// which set joined when they went past.
			if inRun && !joined && !strings.Contains(text[lastEnd:m[0]], ",") {
				flush()
			}
			if !inRun {
				cur = modeRun{start: m[0]}
				inRun = true
			}
			cur.words = append(cur.words, text[m[0]:m[1]])
			lastEnd = m[1]
			joined = false
		case w == "or" || w == "and":
			// A joiner keeps a run open, and only a joiner does.
			joined = true
		default:
			flush()
			joined = false
		}
	}
	flush()
	return out
}

func namesAny(text string, real map[string]bool) bool {
	for _, m := range word.FindAllString(text, -1) {
		if real[strings.ToLower(m)] {
			return true
		}
	}
	return false
}

// sentences splits on terminal punctuation followed by a space. It is not a
// general purpose splitter and does not need to be: a claim about the modes
// that spans a full stop is a claim in two sentences, and each half is checked.
func sentences(flat string) []sentence {
	var out []sentence
	start := 0
	for i := 0; i < len(flat)-1; i++ {
		if (flat[i] == '.' || flat[i] == '!' || flat[i] == '?') && flat[i+1] == ' ' {
			out = append(out, sentence{text: flat[start : i+1], start: start})
			start = i + 2
		}
	}
	if start < len(flat) {
		out = append(out, sentence{text: flat[start:], start: start})
	}
	return out
}

// flatten turns a document into one line and returns a lookup from an offset in
// it back to the original line number. Fenced code in markdown is dropped, for
// prosecheck's reason: a fence holds a manifest fragment, and a manifest
// fragment is the one place a mode name is data rather than a claim.
func flatten(name, body string) (string, func(int) int) {
	lines := strings.Split(body, "\n")
	md := strings.HasSuffix(name, ".md")
	inFence := false

	var b strings.Builder
	starts := make([]int, 0, len(lines))
	nums := make([]int, 0, len(lines))

	for i, line := range lines {
		if md && fence.MatchString(line) {
			inFence = !inFence
			continue
		}
		if md && inFence {
			continue
		}
		starts = append(starts, b.Len())
		nums = append(nums, i+1)
		b.WriteString(line)
		b.WriteByte(' ')
	}

	flat := b.String()
	return flat, func(offset int) int {
		i := sort.Search(len(starts), func(i int) bool { return starts[i] > offset })
		if i == 0 {
			return 1
		}
		return nums[i-1]
	}
}

// excerpt quotes the text around a finding rather than the sentence holding
// it. Sentence splitting is done on a full stop followed by a space, and JSX
// writes "</strong> " and "." against a tag constantly, so a sentence can run
// the width of a whole component. Quoting one put the page's hero copy under a
// finding about its section heading, which sends the reader to the wrong line
// to fix it.
func excerpt(flat string, offset int) string {
	const half = 80
	lo := offset - half
	if lo < 0 {
		lo = 0
	}
	hi := offset + half
	if hi > len(flat) {
		hi = len(flat)
	}
	out := strings.Join(strings.Fields(flat[lo:hi]), " ")
	if lo > 0 {
		out = "..." + out
	}
	if hi < len(flat) {
		out += "..."
	}
	return out
}

// prose lists the documents to check, relative to root.
func prose(root string) ([]string, error) {
	seen := map[string]bool{}
	var out []string

	for _, tree := range trees {
		base := filepath.Join(root, tree)
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				name := d.Name()
				if path != base && (name == "node_modules" || name == "dist" || name == "out" ||
					name == ".next" || name == "vendor" || strings.HasPrefix(name, ".")) {
					return fs.SkipDir
				}
				return nil
			}
			switch filepath.Ext(path) {
			case ".md", ".mdx", ".ts", ".tsx":
			default:
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil || seen[rel] {
				return nil
			}
			seen[rel] = true
			out = append(out, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}
