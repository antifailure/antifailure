package docs

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestBenchmarkTheContextOneAnswerCosts measures what this tool is for.
//
// The claim it has to support is not "an agent can read the documentation".
// It is that an agent can read the PART IT NEEDS without spending the context
// it was going to answer with. So the measurement is a comparison, in the unit
// the caller actually pays in, between what one question costs through the
// tool and what the same question costs by the two routes an agent has
// without it: read the page the answer is in, or read the documentation.
//
// It reports CHARACTERS, exactly, because characters are what this repository
// can measure with no dependency and no network, and because every tokenizer
// is a fixed factor away from them. The report says how to convert them with a
// named tokenizer rather than carrying a guess at the ratio, since a token
// count nobody can reproduce is the kind of number section 3 of the plan bans.
//
// Set AF_DOCS_BENCHMARK_OUT to write the report, and AF_DOCS_BENCHMARK_PAYLOADS
// to a directory to have the exact bytes that were measured written out, so a
// tokenizer can be run over the same input rather than over something similar.
func TestBenchmarkTheContextOneAnswerCosts(t *testing.T) {
	out := os.Getenv("AF_DOCS_BENCHMARK_OUT")
	if out == "" {
		t.Skip("set AF_DOCS_BENCHMARK_OUT to write the report")
	}

	// The question is one an agent actually has to ask about this product,
	// picked because nothing in any model's training data can answer it: the
	// word means something specific here and something else everywhere else.
	const question = "what does stance mean"

	answer := Search(Query{Text: question, MaxResults: 5, MaxChars: defaultBenchmarkBudget})
	require.NotEmpty(t, answer.Hits, "the question must have an answer, or this measures nothing")

	answered := 0
	for _, h := range answer.Hits {
		answered += len(h.Excerpt)
	}

	// What the same question costs the two ways an agent has without this.
	page, ok := Lookup(answer.Hits[0].Path)
	require.True(t, ok)
	wholePage := len(page.Body)

	corpus := 0
	for _, p := range load().pages {
		corpus += len(p.Body)
	}

	// And what it costs to orient without reading anything, which is the call
	// an agent makes before it knows what to search for.
	listing := 0
	for _, l := range List("") {
		listing += len(l.Path) + len(l.Title)
	}

	if dir := os.Getenv("AF_DOCS_BENCHMARK_PAYLOADS"); dir != "" {
		require.NoError(t, os.MkdirAll(dir, 0o755))
		var excerpts strings.Builder
		for _, h := range answer.Hits {
			excerpts.WriteString(h.Excerpt)
			excerpts.WriteString("\n")
		}
		writePayload(t, dir, "answer-excerpts.txt", excerpts.String())
		writePayload(t, dir, "whole-page.txt", page.Body)
		var all strings.Builder
		for _, p := range load().pages {
			all.WriteString(p.Body)
			all.WriteString("\n")
		}
		writePayload(t, dir, "whole-corpus.txt", all.String())
		var index strings.Builder
		for _, l := range List("") {
			fmt.Fprintf(&index, "%s %s\n", l.Path, l.Title)
		}
		writePayload(t, dir, "listing.txt", index.String())
	}

	report := renderBenchmark(question, answer, answered, wholePage, corpus, listing, len(load().pages))
	require.NoError(t, os.MkdirAll(filepath.Dir(out), 0o755))
	require.NoError(t, os.WriteFile(out, []byte(report), 0o644))
	t.Logf("wrote %s", out)
}

// defaultBenchmarkBudget is the tool's own default, so the number measured is
// what a caller gets without asking for anything.
const defaultBenchmarkBudget = 4000

func writePayload(t *testing.T, dir, name, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
}

func renderBenchmark(
	question string, answer Results, answered, wholePage, corpus, listing, pages int,
) string {
	var b strings.Builder
	b.WriteString("# The context one documentation answer costs\n\n")
	fmt.Fprintf(&b, "Run on %s UTC.\n\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "- Machine: %s %s, %d cores.\n", runtime.GOOS, runtime.GOARCH, runtime.NumCPU())
	b.WriteString("- Harness: `just benchmark`, which is " +
		"`engine/internal/docs/benchmark_test.go` in this repository.\n")
	fmt.Fprintf(&b, "- Corpus: the %d pages this build ships, embedded by "+
		"`tools/docsembed`.\n", pages)
	fmt.Fprintf(&b, "- Question: %q, answered by `search_documentation` at its default "+
		"budget of %d characters.\n\n", question, defaultBenchmarkBudget)

	fmt.Fprintf(&b, "| What an agent reads | Characters | Times the answer |\n")
	b.WriteString("| --- | --- | --- |\n")
	rows := []struct {
		what  string
		chars int
	}{
		{fmt.Sprintf("The answer: %d excerpts from %d pages",
			len(answer.Hits), len(answer.Hits)), answered},
		{"The table of contents, path and title per page", listing},
		{fmt.Sprintf("The whole page the answer is on, %s", answer.Hits[0].Path), wholePage},
		{fmt.Sprintf("The whole documentation set, %d pages", pages), corpus},
	}
	for _, r := range rows {
		fmt.Fprintf(&b, "| %s | %s | %.0fx |\n",
			r.what, commas(r.chars), float64(r.chars)/float64(answered))
	}

	fmt.Fprintf(&b, "\nThe answer is **%.2f percent** of the documentation set and "+
		"**%.1f percent** of the one page it came from.\n\n",
		100*float64(answered)/float64(corpus), 100*float64(answered)/float64(wholePage))

	b.WriteString("## Which sections it named, and what it said it left out\n\n")
	for _, h := range answer.Hits {
		fmt.Fprintf(&b, "- `%s` at `#%s`, %s characters. %s\n",
			h.Path, h.Anchor, commas(len(h.Excerpt)), h.Trail)
	}
	fmt.Fprintf(&b, "\n%d pages matched and %d were returned. %d were named as not shown.\n\n",
		answer.PagesMatched, len(answer.Hits), answer.NotShownTotal)

	b.WriteString("## Characters, and how to read them as tokens\n\n")
	b.WriteString("Characters are what this harness measures, because they need no " +
		"tokenizer, no network and no dependency, and because a customer running this " +
		"gets the same number on the same corpus every time. A tokenizer is a fixed " +
		"factor on top, so the ratios in the table above are the same whichever one you " +
		"use.\n\n")
	b.WriteString("To count tokens over the exact bytes this measured rather than over " +
		"something similar, run the test again with a payload directory and count the " +
		"files it writes:\n\n")
	b.WriteString("```sh\nAF_DOCS_BENCHMARK_OUT=/tmp/report.md \\\n" +
		"AF_DOCS_BENCHMARK_PAYLOADS=/tmp/payloads \\\n" +
		"  go test ./internal/docs -run TestBenchmarkTheContextOneAnswerCosts -count=1\n\n" +
		"python3 -c 'import sys,tiktoken; e=tiktoken.get_encoding(\"cl100k_base\"); " +
		"print(len(e.encode(open(sys.argv[1]).read())))' /tmp/payloads/answer-excerpts.txt\n```\n")
	return b.String()
}

// commas renders a count the way a person reads one.
func commas(n int) string {
	digits := fmt.Sprintf("%d", n)
	var parts []string
	for len(digits) > 3 {
		parts = append(parts, digits[len(digits)-3:])
		digits = digits[:len(digits)-3]
	}
	parts = append(parts, digits)
	sort.SliceStable(parts, func(i, j int) bool { return false })
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, ",")
}
