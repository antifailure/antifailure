package change

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os/exec"
	"strconv"
	"strings"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

// MaxDiffBytes bounds how much of a diff is read. Above it the profile is
// truncated, which selects every check: a diff this size has parts nobody
// classified and pretending otherwise is how coverage disappears quietly.
const MaxDiffBytes = 32 << 20

// AddedLine is one line a diff adds, with the line number it will have in the
// new file. The number is carried so that a fact about a line points at
// something a reviewer can open, rather than at an offset into a list.
type AddedLine struct {
	N    int
	Text string
}

// ParseUnifiedDiff reads the output of git diff.
//
// It reads the headers rather than inferring from the body, because the body
// is ambiguous: a removed line and a new file's "--- /dev/null" both begin
// with a minus, and a file whose contents are a diff would defeat any parser
// that guessed. Everything this returns comes from a line git wrote to say
// what it was about to do.
func ParseUnifiedDiff(r io.Reader) ([]File, bool, error) {
	sc := bufio.NewScanner(io.LimitReader(r, MaxDiffBytes+1))
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)

	var (
		files     []File
		cur       *File
		read      int64
		truncated bool
		newLine   int
	)
	flush := func() {
		if cur != nil {
			files = append(files, *cur)
			cur = nil
		}
	}
	for sc.Scan() {
		line := sc.Text()
		read += int64(len(line)) + 1
		if read > MaxDiffBytes {
			truncated = true
			break
		}
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flush()
			a, b := splitDiffHeader(strings.TrimPrefix(line, "diff --git "))
			cur = &File{Path: b, Status: StatusModified}
			if b == "" {
				cur.Path = a
			}
			newLine = 0
		case cur == nil:
			// Anything before the first header, such as a commit message when
			// somebody pipes git show, is not part of a file.
			continue

		// Everything below is split on whether a hunk has started, and that is
		// not tidiness. Removing the line "-- end" produces the diff line
		// "--- end", and adding the line "++ x" produces "+++ x", so inside a
		// hunk the header prefixes are content. A parser that matched them
		// anywhere would read a file's own text as a new file header, which is
		// the classic way a diff parser silently loses half a change.
		case newLine == 0:
			switch {
			case strings.HasPrefix(line, "new file mode "):
				cur.Status = StatusAdded
			case strings.HasPrefix(line, "deleted file mode "):
				cur.Status = StatusDeleted
			case strings.HasPrefix(line, "rename from "):
				cur.Status = StatusRenamed
				cur.OldPath = dequote(strings.TrimPrefix(line, "rename from "))
			case strings.HasPrefix(line, "rename to "):
				cur.Status = StatusRenamed
				cur.Path = dequote(strings.TrimPrefix(line, "rename to "))
			case strings.HasPrefix(line, "Binary files ") || strings.HasPrefix(line, "GIT binary patch"):
				cur.Binary = true
			case strings.HasPrefix(line, "--- "):
				if p := trimSide(line[4:]); p != "" && cur.Status == StatusDeleted {
					cur.Path = p
				}
			case strings.HasPrefix(line, "+++ "):
				if p := trimSide(line[4:]); p != "" {
					cur.Path = p
				}
			case strings.HasPrefix(line, "@@"):
				newLine = hunkStart(line)
			}

		case strings.HasPrefix(line, "@@"):
			newLine = hunkStart(line)
		case strings.HasPrefix(line, "+"):
			cur.Added++
			if len(cur.AddedLines) < MaxAddedLines {
				cur.AddedLines = append(cur.AddedLines, AddedLine{N: newLine, Text: line[1:]})
			} else {
				cur.LinesTruncated = true
			}
			newLine++
		case strings.HasPrefix(line, "-"):
			cur.Removed++
		case strings.HasPrefix(line, "\\"):
			// "\ No newline at end of file" belongs to the line before it.
			continue
		default:
			// A context line, which only appears when the diff was produced
			// with context. It advances the new file's line number.
			newLine++
		}
	}
	flush()
	if err := sc.Err(); err != nil {
		return nil, truncated, err
	}
	return files, truncated, nil
}

// splitDiffHeader splits "a/x b/y" into the two paths, coping with the case
// git makes hard: a path containing a space. Git quotes such a path, so the
// quoted form is unambiguous; the unquoted form is split on the "b/" that
// begins the second half.
func splitDiffHeader(s string) (string, string) {
	if strings.HasPrefix(s, `"`) {
		a, rest := scanQuoted(s)
		rest = strings.TrimSpace(rest)
		b, _ := scanQuoted(rest)
		if b == "" {
			b = rest
		}
		return trimSide(a), trimSide(b)
	}
	if i := strings.Index(s, " b/"); i > 0 {
		return trimSide(s[:i]), trimSide(s[i+1:])
	}
	if i := strings.LastIndex(s, " "); i > 0 {
		return trimSide(s[:i]), trimSide(s[i+1:])
	}
	return trimSide(s), ""
}

func scanQuoted(s string) (string, string) {
	if !strings.HasPrefix(s, `"`) {
		if i := strings.IndexByte(s, ' '); i >= 0 {
			return s[:i], s[i:]
		}
		return s, ""
	}
	for i := 1; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == '"' {
			return dequote(s[:i+1]), s[i+1:]
		}
	}
	return dequote(s), ""
}

// trimSide removes the a/ or b/ prefix git puts on a path, and returns empty
// for /dev/null.
func trimSide(s string) string {
	s = dequote(strings.TrimSpace(s))
	// A tab separates the path from a timestamp in some diff dialects.
	if i := strings.IndexByte(s, '\t'); i >= 0 {
		s = s[:i]
	}
	if s == "/dev/null" {
		return ""
	}
	if len(s) > 2 && (s[0] == 'a' || s[0] == 'b') && s[1] == '/' {
		return s[2:]
	}
	return s
}

// dequote undoes git's C style quoting of a path with unusual bytes in it.
func dequote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return s
	}
	if out, err := strconv.Unquote(s); err == nil {
		return out
	}
	return s[1 : len(s)-1]
}

// hunkStart reads the new file's starting line from a hunk header. A header
// that cannot be read returns 1, which keeps the parser going with line
// numbers that are wrong rather than dropping the hunk's added lines.
func hunkStart(line string) int {
	i := strings.IndexByte(line, '+')
	if i < 0 {
		return 1
	}
	rest := line[i+1:]
	end := strings.IndexAny(rest, ", @")
	if end < 0 {
		end = len(rest)
	}
	n, err := strconv.Atoi(rest[:end])
	if err != nil || n < 1 {
		return 1
	}
	return n
}

// GitOptions are the inputs to reading a diff out of a checkout.
type GitOptions struct {
	// Dir is the repository.
	Dir string
	// Base is the ref this change is measured against, and Head is the ref
	// being measured.
	Base, Head string
}

// FromGit returns the diff between two refs.
//
// The three dot form is deliberate. Two dots would compare the tips, so every
// commit landed on the base branch since this one forked would appear in the
// profile as though this change made it. Three dots compares against the
// merge base, which is the set of files this change is actually responsible
// for, and is what a pull request shows.
func FromGit(ctx context.Context, opts GitOptions) ([]File, bool, error) {
	head := opts.Head
	if head == "" {
		head = "HEAD"
	}
	args := []string{
		"-C", opts.Dir, "diff", "--no-color", "--no-ext-diff", "--find-renames",
		"--unified=0", opts.Base + "..." + head,
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, false, aferrors.Coded(aferrors.AFDET010,
			"base", opts.Base, "head", head, "detail", gitError(err))
	}
	return ParseUnifiedDiff(strings.NewReader(string(out)))
}

// ResolveBase picks the ref a change is measured against.
//
// The order is the order the answer is most likely to be right in, and every
// candidate is verified to exist in this checkout before it is returned. A
// base that does not resolve is the common failure here: a CI job that clones
// one commit deep has no base branch at all, and the error says so rather
// than diffing against nothing.
func ResolveBase(ctx context.Context, dir string, getenv func(string) string) (string, error) {
	var candidates []string
	if getenv != nil {
		if ref := getenv("GITHUB_BASE_REF"); ref != "" {
			candidates = append(candidates, "origin/"+ref, ref)
		}
	}
	candidates = append(candidates,
		"origin/HEAD", "origin/main", "origin/master", "main", "master")
	for _, c := range candidates {
		if refExists(ctx, dir, c) {
			return c, nil
		}
	}
	return "", aferrors.Coded(aferrors.AFDET010,
		"base", strings.Join(candidates, ", "), "head", "HEAD",
		"detail", "none of the usual base refs exist in this checkout")
}

func refExists(ctx context.Context, dir, ref string) bool {
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return cmd.Run() == nil
}

// gitError returns what git wrote to stderr, which is the only part of an
// exec failure worth showing somebody.
func gitError(err error) string {
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		return strings.TrimSpace(string(ee.Stderr))
	}
	return err.Error()
}
