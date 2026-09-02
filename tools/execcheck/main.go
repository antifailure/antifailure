// Command execcheck refuses a script that something runs by path and that git
// does not record as executable.
//
// It exists because tools/site/check-tls.sh was committed at mode 100644. Both
// of the places that run it name it as a bare relative path, the certificate
// step in .github/workflows/deploy.yml and the `check-tls` recipe in the
// justfile, so the kernel refused to exec it and the step died with "Permission
// denied" and status 126. The script had never run anywhere. The only job that
// calls it fires on a push to main and not on a pull request, so the first push
// that reached the deploy job was also the first time anybody learned that the
// certificate check had never once checked a certificate.
//
// The mode is read from the git index rather than from the filesystem, and that
// is the whole point of the tool rather than a detail of it. A developer who
// runs `chmod +x` without `git update-index --chmod=+x` has a working tree that
// executes the script and a commit that does not, so a stat of the disk answers
// yes on the machine where the bug was introduced and CI, which checks out the
// index into a fresh tree, answers no. A gate whose verdict depends on what you
// happen to have chmodded is not a gate.
//
// TWO RULES, because either one alone leaves a hole the other covers.
//
//  1. Shape. Every tracked file named *.sh that opens with a shebang carries
//     the executable bit. This needs no parser and no call site: a file that
//     declares an interpreter is a program. The tree measured fifteen tracked
//     .sh files when the rule was written, every one of them with a shebang,
//     and only check-tls.sh without the bit, so the rule cost nobody a change.
//     It catches a script committed wrong before anything runs it, which is
//     the state check-tls.sh sat in for as long as it existed.
//
//  2. Call site. Every path that a `run:` block in .github/workflows or a
//     justfile recipe puts in command position, and that names a tracked file,
//     carries the bit too. This catches the case the suffix rule cannot see, a
//     program with no .sh on the end of its name, and it is the rule that
//     states the actual invariant: what makes the bit necessary is that
//     something execs the file.
//
// Command position is the load bearing idea in the second rule and it is why
// this does not simply grep for filenames. `bash tools/x.sh` and `source
// tools/x.sh` and `cat tools/x.sh` all name the script and none of them need
// the bit, because the first word of the command is what the kernel is asked
// to exec. So only the first token of each command is read, and a token with no
// slash in it is skipped, because that is a name resolved on PATH rather than a
// file in this repository.
//
// WHAT THIS DELIBERATELY DOES NOT CHECK, because a gate that cries wolf gets
// switched off. Four tracked files carry a shebang without the bit and none of
// them is a defect. deploy/docker/personas.mjs is read by a test rather than
// run. web/apps/api/src/backup-scratch.ts is run as `node
// web/apps/api/src/backup-scratch.ts`, through its interpreter, so the shebang
// is decorative. The other two are the `bin` targets of their packages, and
// npm sets the mode on a bin target when it links it, so the bit is not what
// makes `af-runner` work. Worth recording rather than gating: runner's bin
// target is 100644 and the control plane's is 100755, which is two packages of
// the same shape disagreeing, and it is an inconsistency rather than a
// breakage. A rule demanding a bit that nothing needs would be noise, and
// noise is how a gate earns the reputation that gets it deleted.
//
// BOTH RULES REFUSE TO PASS OVER NOTHING. A gate that matches nothing reports
// green, which is exactly how the certificate check kept its 100644 through
// every CI run that has ever happened. So finding no shell scripts, no
// workflows, no recipes, or no invocations at all is an error rather than a
// clean bill of health, and one test drives the extractor over this
// repository's own files and demands a known invocation come back.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// executable is the git mode a program has to carry. git records only two modes
// for a regular file, so this is the whole of the question.
const executable = "100755"

// regular is the git mode of a tracked file that is not executable. A symlink
// is 120000 and a submodule is 160000; neither is a file whose bit this tool
// has any business having an opinion about.
const regular = "100644"

// exceptions lists tracked *.sh files that carry a shebang, are deliberately
// not executable, and say why. It is empty, and that is a measurement rather
// than an oversight: on the day this was written every one of the fifteen shell
// scripts in the tree except the one that caused the incident was already
// 100755, so the rule cost nobody an entry.
//
// An entry here that no longer describes a real defect fails the build, the
// same way a stale vulnerability suppression does. A file whose mode has since
// been fixed, or that has been deleted, must lose its exception rather than
// keep silently covering whatever takes that path next.
//
// Note what an exception does not do. It excuses a file from the shape rule
// only. If a workflow or a recipe actually runs the file, the call site rule
// still refuses it, because at that point the mode is not a matter of taste.
var exceptions = map[string]string{}

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	if args := flag.Args(); len(args) > 0 {
		*root = args[0]
	}

	if err := run(*root, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "\nexeccheck: %v\n", err)
		os.Exit(1)
	}
}

// finding is one file that needs the executable bit and does not have it.
type finding struct {
	// file is the tracked path, which is what the fix names.
	file string
	// mode is what git records for it now.
	mode string
	// why says what makes the bit necessary, in the words of whichever rule
	// caught it. A reader who disagrees with the verdict needs to know which
	// of the two rules they are arguing with.
	why string
}

func run(root string, out io.Writer) error {
	modes, err := trackedModes(root)
	if err != nil {
		return err
	}
	if len(modes) == 0 {
		return fmt.Errorf("git listed no tracked files under %s, so this check is looking in the wrong place", root)
	}

	shape, scanned, used, err := shapeFindings(root, modes)
	if err != nil {
		return err
	}
	if scanned == 0 {
		return fmt.Errorf("found no tracked shell script under %s, so this check is looking in the wrong place", root)
	}
	if stale := unused(used); len(stale) > 0 {
		return fmt.Errorf("%d %s in exceptions no longer needs one, so it covers whatever takes that path next: %s",
			len(stale), plural(len(stale), "file", "files"), strings.Join(stale, ", "))
	}

	calls, err := invocations(root, modes)
	if err != nil {
		return err
	}
	if len(calls) == 0 {
		return fmt.Errorf("found no script run by path in any workflow or recipe under %s, so this check is looking in the wrong place", root)
	}

	found := shape
	for _, c := range calls {
		if modes[c.script] == executable {
			continue
		}
		found = append(found, finding{
			file: c.script,
			mode: modes[c.script],
			why:  fmt.Sprintf("%s:%d runs it as `%s`", c.source, c.line, c.text),
		})
	}

	// Sorted by file, and within a file the shape rule's verdict comes first
	// because it is the shorter sentence. A file caught by both rules says both
	// things: one reader wants to know the mode is wrong and the other wants to
	// know which line is about to fail because of it.
	sort.SliceStable(found, func(i, j int) bool { return found[i].file < found[j].file })
	files := map[string]bool{}
	for _, f := range found {
		files[f.file] = true
	}

	// Write errors are ignored explicitly and once, for the reason prosecheck
	// gives: the verdict of this tool is its exit code, not its report, so a
	// broken pipe changes what a person can read and not whether the build
	// should fail.
	report := func(format string, args ...any) { _, _ = fmt.Fprintf(out, format, args...) }

	last := ""
	for _, f := range found {
		if f.file != last {
			report("%s is mode %s, and git checks it out without the executable bit.\n", f.file, f.mode)
			report("    Fix it with: git update-index --chmod=+x %s\n", f.file)
			last = f.file
		}
		report("    %s\n", f.why)
	}
	report("execcheck: %d shell %s, %d %s by path, %d %s not executable\n",
		scanned, plural(scanned, "script", "scripts"),
		len(calls), plural(len(calls), "invocation", "invocations"),
		len(files), plural(len(files), "file", "files"))

	if len(files) > 0 {
		return fmt.Errorf("%d %s cannot be executed by the command that runs it",
			len(files), plural(len(files), "file", "files"))
	}
	return nil
}

// shapeFindings applies the first rule: a tracked *.sh that opens with a
// shebang is a program, so git has to record it as one.
//
// It returns the findings, how many scripts it looked at, and which exceptions
// it needed, so that the caller can refuse both an empty scan and a stale
// exception.
func shapeFindings(root string, modes map[string]string) (found []finding, scanned int, used map[string]bool, err error) {
	used = map[string]bool{}

	var paths []string
	for p := range modes {
		if strings.HasSuffix(p, ".sh") {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)

	for _, p := range paths {
		if modes[p] != executable && modes[p] != regular {
			continue // a symlink or a submodule, not a file with a bit to set
		}
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil {
			return nil, 0, nil, fmt.Errorf("reading %s: %w", p, err)
		}
		if !strings.HasPrefix(string(body), "#!") {
			continue
		}
		scanned++
		if modes[p] == executable {
			continue
		}
		if _, ok := exceptions[p]; ok {
			used[p] = true
			continue
		}
		found = append(found, finding{
			file: p,
			mode: modes[p],
			why:  "opens with a shebang, so it is a program",
		})
	}
	return found, scanned, used, nil
}

// unused returns the exceptions that no file needed, sorted.
func unused(used map[string]bool) []string {
	var stale []string
	for p := range exceptions {
		if !used[p] {
			stale = append(stale, p)
		}
	}
	sort.Strings(stale)
	return stale
}

// trackedModes asks git for the mode of every tracked file.
//
// `git ls-files -s` prints "<mode> <object> <stage>\t<path>", and -z ends each
// record with a NUL so that a path with a space or a newline in it stays one
// record.
func trackedModes(root string) (map[string]string, error) {
	out, err := exec.Command("git", "-C", root, "ls-files", "-s", "-z").Output()
	if err != nil {
		return nil, fmt.Errorf("asking git for the mode of every tracked file: %w", err)
	}
	return parseLsFiles(string(out)), nil
}

// parseLsFiles reads the output of `git ls-files -s -z`.
func parseLsFiles(out string) map[string]string {
	modes := map[string]string{}
	for _, rec := range strings.Split(out, "\x00") {
		if rec == "" {
			continue
		}
		tab := strings.IndexByte(rec, '\t')
		if tab < 0 {
			continue
		}
		fields := strings.Fields(rec[:tab])
		if len(fields) == 0 {
			continue
		}
		modes[rec[tab+1:]] = fields[0]
	}
	return modes
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
