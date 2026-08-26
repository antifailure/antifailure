package build

import (
	"bufio"
	"io"
	"regexp"
	"strings"
	"testing"
	"unicode"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

func parse(t *testing.T, body string) *Ignore {
	t.Helper()
	ig, err := ParseIgnore(strings.NewReader(body))
	require.NoError(t, err)
	return ig
}

func TestIgnore_EmptyExcludesNothing(t *testing.T) {
	t.Parallel()
	for _, body := range []string{"", "\n\n", "# just a comment\n", "   \n\t\n"} {
		ig := parse(t, body)
		got, _ := ig.Excluded("src/index.ts", false)
		require.False(t, got, "body %q", body)
	}
	nilIg, err := ParseIgnore(nil)
	require.NoError(t, err)
	got, _ := nilIg.Excluded("anything", false)
	require.False(t, got)
}

func TestIgnore_ADirectoryExcludesEverythingUnderIt(t *testing.T) {
	t.Parallel()
	ig := parse(t, "node_modules\n")
	for _, p := range []string{
		"node_modules",
		"node_modules/react",
		"node_modules/react/index.js",
		"node_modules/a/b/c/d/e.js",
	} {
		got, by := ig.Excluded(p, false)
		require.True(t, got, "path %q must be excluded", p)
		require.Equal(t, "node_modules", by)
	}
	// And nothing that merely starts with the same letters.
	got, _ := ig.Excluded("node_modules_backup/x", false)
	require.False(t, got)
	got, _ = ig.Excluded("src/node_modules_notes.md", false)
	require.False(t, got)
}

func TestIgnore_SingleStarStopsAtASeparator(t *testing.T) {
	t.Parallel()
	// The distinction that matters. Treating * like ** is the mistake that
	// silently sends a nested node_modules to the daemon.
	ig := parse(t, "src/*\n")
	got, _ := ig.Excluded("src/a.ts", false)
	require.True(t, got)
	// One level down is still under src, so the directory rule reaches it.
	got, _ = ig.Excluded("src/deep/a.ts", false)
	require.True(t, got, "src/deep matches src/*, so its contents go too")
	got, _ = ig.Excluded("other/a.ts", false)
	require.False(t, got)
}

func TestIgnore_DoubleStarCrossesSeparators(t *testing.T) {
	t.Parallel()
	ig := parse(t, "**/node_modules\n")
	for _, p := range []string{
		"node_modules/x",
		"packages/web/node_modules/react/index.js",
		"a/b/c/node_modules",
	} {
		got, _ := ig.Excluded(p, false)
		require.True(t, got, "path %q", p)
	}
	got, _ := ig.Excluded("packages/web/src/index.ts", false)
	require.False(t, got)
}

func TestIgnore_DoubleStarInTheMiddle(t *testing.T) {
	t.Parallel()
	ig := parse(t, "packages/**/dist\n")
	got, _ := ig.Excluded("packages/web/dist/main.js", false)
	require.True(t, got)
	got, _ = ig.Excluded("packages/dist/main.js", false)
	require.True(t, got, "** also matches nothing at all")
	got, _ = ig.Excluded("packages/web/src/main.js", false)
	require.False(t, got)
}

func TestIgnore_TrailingDoubleStar(t *testing.T) {
	t.Parallel()
	ig := parse(t, "logs/**\n")
	got, _ := ig.Excluded("logs/a/b.txt", false)
	require.True(t, got)
	got, _ = ig.Excluded("logsx/a.txt", false)
	require.False(t, got)
}

func TestIgnore_QuestionMarkMatchesOneCharacterNotASeparator(t *testing.T) {
	t.Parallel()
	ig := parse(t, "a?.txt\n")
	got, _ := ig.Excluded("ab.txt", false)
	require.True(t, got)
	got, _ = ig.Excluded("abc.txt", false)
	require.False(t, got)
	got, _ = ig.Excluded("a/.txt", false)
	require.False(t, got, "? does not cross a separator")
}

func TestIgnore_LastMatchingPatternWins(t *testing.T) {
	t.Parallel()
	// The rule that makes exceptions work at all.
	ig := parse(t, "node_modules\n!node_modules/.keep\n")
	got, _ := ig.Excluded("node_modules/react/index.js", false)
	require.True(t, got)
	got, by := ig.Excluded("node_modules/.keep", false)
	require.False(t, got, "the later exception wins")
	require.Equal(t, "!node_modules/.keep", by,
		"the deciding pattern is reported even when it decided to keep, because "+
			"why a file survived is the harder question to answer")

	// And order is what decides, not negation.
	reversed := parse(t, "!node_modules/.keep\nnode_modules\n")
	got, _ = reversed.Excluded("node_modules/.keep", false)
	require.True(t, got, "written the other way round, the exclusion is later and wins")
}

func TestIgnore_ATrailingSlashMatchesOnlyADirectory(t *testing.T) {
	t.Parallel()
	ig := parse(t, "build/\n")
	got, _ := ig.Excluded("build", true)
	require.True(t, got)
	got, _ = ig.Excluded("build", false)
	require.False(t, got, "a file called build is not the directory the pattern names")
	got, _ = ig.Excluded("build/out.js", false)
	require.True(t, got, "but a file inside the directory is still excluded")
}

func TestIgnore_LeadingSlashAndDotSlashAreTheSameAsBare(t *testing.T) {
	t.Parallel()
	// Every form appears in real .dockerignore files. They all mean the same
	// thing, and a reader who writes one form and gets a different result has
	// no way to discover why.
	for _, pattern := range []string{"dist", "/dist", "./dist"} {
		ig := parse(t, pattern+"\n")
		got, _ := ig.Excluded("dist/main.js", false)
		require.True(t, got, "pattern %q", pattern)
	}
}

func TestIgnore_DockerignoreIsAlwaysSent(t *testing.T) {
	t.Parallel()
	// The daemon reads it to report what was excluded. A .dockerignore that
	// excluded itself would produce a build whose exclusions cannot be
	// explained.
	ig := parse(t, "*\n")
	got, _ := ig.Excluded(".dockerignore", false)
	require.False(t, got)
	got, _ = ig.Excluded("anything-else", false)
	require.True(t, got)
}

func TestIgnore_RefusesAPatternThatWouldExcludeEverything(t *testing.T) {
	t.Parallel()
	// A context with nothing in it produces a build failure whose message is
	// about a missing Dockerfile, which sends the reader looking in entirely
	// the wrong place.
	for _, body := range []string{".", "./", "/", "  .  "} {
		_, err := ParseIgnore(strings.NewReader(body))
		require.Error(t, err, "body %q", body)
		require.Contains(t, err.Error(), "exclude everything")
		require.Contains(t, err.Error(), "line 1")
	}
}

func TestIgnore_RegexMetacharactersAreLiteral(t *testing.T) {
	t.Parallel()
	// A dot in a filename is a dot, not "any character". Somebody with a
	// coverage.out and a coverageXout would otherwise lose both.
	ig := parse(t, "coverage.out\n")
	got, _ := ig.Excluded("coverage.out", false)
	require.True(t, got)
	got, _ = ig.Excluded("coverageXout", false)
	require.False(t, got)

	plus := parse(t, "a+b.log\n")
	got, _ = plus.Excluded("a+b.log", false)
	require.True(t, got)
	got, _ = plus.Excluded("aab.log", false)
	require.False(t, got)
}

func TestIgnore_ReportsThePatternsAsWritten(t *testing.T) {
	t.Parallel()
	ig := parse(t, "node_modules\n# comment\n!node_modules/.keep\n\n.git\n")
	require.Equal(t, []string{"node_modules", "!node_modules/.keep", ".git"}, ig.Patterns())
}

func TestIgnore_RealisticFileBehavesAsAWhole(t *testing.T) {
	t.Parallel()
	ig := parse(t, `# Generated
node_modules
**/node_modules
.next
dist/
coverage/
*.log
.git
.env
!.env.example
`)
	excluded := []string{
		"node_modules/react/index.js",
		"packages/web/node_modules/x.js",
		".next/server/app.js",
		"dist/main.js",
		"coverage/lcov.info",
		"debug.log",
		".git/HEAD",
		".env",
	}
	for _, p := range excluded {
		got, _ := ig.Excluded(p, false)
		require.True(t, got, "%q must be excluded", p)
	}
	kept := []string{
		"src/index.ts",
		"package.json",
		"Dockerfile",
		".env.example",
		"docs/dist-notes.md",
	}
	for _, p := range kept {
		got, by := ig.Excluded(p, false)
		require.False(t, got, "%q must be kept, but %q excluded it", p, by)
	}
}

// errReader fails partway through, which is what a truncated file or a closed
// pipe looks like to the scanner.
type errReader struct {
	body string
	n    int
}

func (e *errReader) Read(p []byte) (int, error) {
	if e.n > 0 {
		return 0, io.ErrUnexpectedEOF
	}
	e.n = copy(p, e.body)
	return e.n, nil
}

func TestIgnore_ReportsAReadFailureRatherThanSilentlyIgnoringLess(t *testing.T) {
	t.Parallel()
	// A truncated .dockerignore that parsed as far as it got would produce a
	// context missing some exclusions and nothing to say so, which shows up
	// as a build that is mysteriously slow.
	_, err := ParseIgnore(&errReader{body: "node_modules\n"})
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
}

func TestGlobToRegexp_AlwaysProducesSomethingThatCompiles(t *testing.T) {
	t.Parallel()
	// The compile guard in compileIgnore protects this property rather than
	// any input a user can supply: every character except * ? and / is
	// escaped, so the output is always valid today. The guard stays because
	// the day somebody adds a case to globToRegexp is the day it stops being
	// true, and a MustCompile there would turn that into a panic in the
	// middle of somebody's build.
	rapid.Check(t, func(rt *rapid.T) {
		p := rapid.StringOfN(rapid.RuneFrom([]rune("ab*?/.[](){}|+^$\\-_ \t"), unicode.Latin), 0, 40, -1).
			Draw(rt, "pattern")
		re, err := regexp.Compile("^" + globToRegexp(p) + "$")
		if err != nil {
			rt.Fatalf("globToRegexp(%q) does not compile: %v", p, err)
		}
		// And it never panics on a match, which is the other way a bad
		// expression shows up.
		re.MatchString("src/index.ts")
	})
}

func TestIgnore_RefusesALineTooLongToRead(t *testing.T) {
	t.Parallel()
	// The scanner is bounded so that a file with no newlines cannot be read
	// into memory without limit. Hitting the bound is an error rather than a
	// silent truncation, because a truncated pattern would exclude the wrong
	// things.
	_, err := ParseIgnore(strings.NewReader(strings.Repeat("a", 2<<20)))
	require.Error(t, err)
	require.ErrorIs(t, err, bufio.ErrTooLong)
}
