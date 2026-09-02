package main

import (
	"reflect"
	"strings"
	"testing"
)

// The first word of a command is the file the kernel is asked to exec, and
// every other position names a file that some other program opens. Getting this
// distinction wrong in either direction is how a mode gate becomes noise: too
// strict and it demands the bit on a config file, too loose and it misses the
// script that actually fails.
func TestCommandTokensReadsOnlyCommandPosition(t *testing.T) {
	cases := []struct {
		line string
		want []string
	}{
		{"tools/site/check-tls.sh", []string{"tools/site/check-tls.sh"}},
		{"./tools/site/check-tls.sh --verbose", []string{"./tools/site/check-tls.sh"}},

		// The three ways to name a script without execing it. None of them
		// needs the bit, and the head of each is a name on PATH.
		{"bash tools/site/check-tls.sh", []string{"bash"}},
		{"source tools/site/lib.sh", []string{"source"}},
		{"cat tools/site/check-tls.sh > /dev/null", []string{"cat"}},

		// Words that stand in front of the command they run.
		{"sudo tools/site/check-tls.sh", []string{"tools/site/check-tls.sh"}},
		{"exec tools/site/check-tls.sh", []string{"tools/site/check-tls.sh"}},
		{"AF_ENV=ci NODE_ENV=test tools/site/check-tls.sh", []string{"tools/site/check-tls.sh"}},
		{"if tools/site/check-tls.sh; then echo ok; fi", []string{"tools/site/check-tls.sh", "echo", "fi"}},

		// One line, several commands.
		{"tools/a.sh && tools/b.sh", []string{"tools/a.sh", "tools/b.sh"}},
		{"tools/a.sh | tools/b.sh", []string{"tools/a.sh", "tools/b.sh"}},
		{"(cd www && tools/a.sh)", []string{"cd", "tools/a.sh"}},

		// just's line prefixes are not part of the command.
		{"@tools/site/check-tls.sh", []string{"tools/site/check-tls.sh"}},
		{"-tools/site/check-tls.sh", []string{"tools/site/check-tls.sh"}},

		// A shell comment is not a command.
		{"# tools/site/check-tls.sh runs after a publish", nil},

		// A `${...}` is one value. Splitting at its braces produced the only
		// false match this tool ever made on this repository, reading
		// `dir%/package-lock.json` out of the justfile as a command.
		{`[ -f "${dir%/}/package-lock.json" ] || exit 1`, []string{"[", "exit"}},
	}

	for _, c := range cases {
		got := commandTokens(c.line)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("commandTokens(%q)\n got %q\nwant %q", c.line, got, c.want)
		}
	}
}

// A token this cannot resolve to a tracked file is not a finding, and a token
// that resolves to twelve of them by its bare name is not one either.
func TestResolve(t *testing.T) {
	index := suffixIndex(map[string]string{
		"tools/site/check-tls.sh": regular,
		"deploy/status/probe.sh":  executable,
		"install.sh":              executable,
		"www/package-lock.json":   regular,
		"docs/package-lock.json":  regular,
		"docs/link":               "120000",
		"engine/cmd/af/main.go":   regular,
	})

	cases := []struct {
		tok  string
		want []string
	}{
		{"tools/site/check-tls.sh", []string{"tools/site/check-tls.sh"}},
		{"./tools/site/check-tls.sh", []string{"tools/site/check-tls.sh"}},

		// The status page's job checks this tree out into `main/`, so its own
		// spelling of the path carries a leading segment the repository does
		// not have.
		{"main/deploy/status/probe.sh", []string{"deploy/status/probe.sh"}},

		// A path at the repository root, named the only way a shell can name
		// it, resolves even though nothing is left of it but a file name.
		{"./install.sh", []string{"install.sh"}},

		// A bare file name that two trees share is not an invocation of either.
		{"${dir}/package-lock.json", nil},
		{"dir/package-lock.json", nil},

		// Not paths in this repository.
		{"go", nil},
		{"npm", nil},
		{"-v", nil},
		{"/usr/bin/env", nil},
		{"../outside/thing.sh", nil},
		{"dist/*.sh", nil},
		{"tools/site/nothing-here.sh", nil},

		// A symlink is not a file whose bit this has an opinion about.
		{"docs/link", nil},
	}

	for _, c := range cases {
		got := resolve(c.tok, index)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("resolve(%q) = %q, want %q", c.tok, got, c.want)
		}
	}
}

func TestWorkflowShellReadsRunBlocksAndTheirLineNumbers(t *testing.T) {
	source := `name: ci
on: [push]
jobs:
  gates:
    steps:
      - uses: actions/checkout@v5
      - name: One line
        run: tools/site/assemble.sh
      - name: A block
        run: |
          cd www
          tools/site/check-tls.sh
      - name: After the block
        uses: actions/upload-artifact@v4
        with:
          path: tools/site/not-a-command.sh
`
	got := workflowShell(source)
	var lines []string
	for _, l := range got {
		if strings.TrimSpace(l.text) != "" {
			lines = append(lines, l.text)
		}
	}
	want := []string{"tools/site/assemble.sh", "          cd www", "          tools/site/check-tls.sh"}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("workflowShell read\n %q\nwant %q", lines, want)
	}
	if got[0].num != 8 {
		t.Errorf("the one line run is at line %d, want 8", got[0].num)
	}
	if got[2].num != 12 {
		t.Errorf("the second line of the block is at line %d, want 12", got[2].num)
	}
}

// A `with:` block is not shell. A step that passes a script path to an action
// is not running it, and reading the whole file as one stream would say it was.
func TestWorkflowShellStopsAtTheEndOfTheBlock(t *testing.T) {
	source := `jobs:
  gates:
    steps:
      - name: A block
        run: |
          echo hello
      - name: Not shell
        with:
          path: tools/site/check-tls.sh
`
	for _, l := range workflowShell(source) {
		if strings.Contains(l.text, "check-tls") {
			t.Fatalf("a `with:` value was read as shell: %q", l.text)
		}
	}
}

func TestJustShellReadsRecipeBodiesOnly(t *testing.T) {
	source := `set shell := ["bash", "-uc"]

reports := ".gate-reports"

# A doc comment between two recipes does not end a body.
check-tls:
    tools/site/check-tls.sh

# ---------------------------------------------------------------------------
# A divider does not end one either.
# ---------------------------------------------------------------------------
site:
    tools/site/assemble.sh
`
	var lines []string
	for _, l := range justShell(source) {
		lines = append(lines, strings.TrimSpace(l.text))
	}
	want := []string{"tools/site/check-tls.sh", "tools/site/assemble.sh"}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("justShell read %q, want %q", lines, want)
	}
}

// A heredoc body is data, and a continuation carries arguments. Reading either
// as a command is how a tool like this starts reporting things nobody runs.
func TestCommandLinesSkipsHeredocsAndContinuations(t *testing.T) {
	lines := []shellLine{
		{1, "cat > config.yaml <<'EOF'"},
		{2, "tools/site/check-tls.sh"},
		{3, "EOF"},
		{4, `go run ./tools/relnotes \`},
		{5, "  --out tools/site/notes.sh"},
		{6, "tools/site/assemble.sh"},
	}

	var got []int
	for _, l := range commandLines(lines) {
		got = append(got, l.num)
	}
	if want := []int{1, 4, 6}; !reflect.DeepEqual(got, want) {
		t.Fatalf("commandLines kept lines %v, want %v", got, want)
	}
}

// A here string puts its body on the same line, so it does not open a document
// and the next line is still a command.
func TestCommandLinesDoesNotTakeAHereStringForAHeredoc(t *testing.T) {
	lines := []shellLine{
		{1, "grep -q ok <<< \"$body\""},
		{2, "tools/site/assemble.sh"},
	}
	if got := commandLines(lines); len(got) != 2 {
		t.Fatalf("a here string swallowed the next line: %v", got)
	}
}

// A `<<` inside quotes is text, and reading it as a redirection would open a
// document whose delimiter never arrives, swallowing every command after it in
// the block. The result would be a quiet zero, which reads exactly like a clean
// repository.
func TestOpenHeredoc(t *testing.T) {
	cases := map[string]string{
		"cat > f <<'EOF'":               "EOF",
		"cat > f <<EOF":                 "EOF",
		`cat > f <<-"SQL"`:              "SQL",
		`echo "use << to redirect"`:     "",
		"echo 'a << b'":                 "",
		`grep -q ok <<< "$body"`:        "",
		"tools/site/assemble.sh":        "",
		`printf '%s' "$x" > f <<MARKER`: "MARKER",
	}
	for line, want := range cases {
		if got := openHeredoc(line); got != want {
			t.Errorf("openHeredoc(%q) = %q, want %q", line, got, want)
		}
	}
}
