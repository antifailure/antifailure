// Command relnotes builds a release's notes, and refuses to build empty ones.
//
// The release published `generate_release_notes: true`, which is a list of
// merged pull requests. Over a release spanning hundreds of landings that is a
// wall, and it is the first thing somebody deciding whether to buy this reads.
// So the notes are written by hand in CHANGELOG.md and this reads the section
// for the tag.
//
// It also owns the verification preamble, and that is the whole reason this is
// a command rather than three lines of shell. softprops/action-gh-release tries
// `body_path` first and falls back to `body` only when the path cannot be read.
// So the moment a notes file reads successfully, the `body:` block is dropped,
// silently, with the step still green. The preamble carrying the cosign
// verify-blob command a person is told to run would have vanished from every
// release note and nothing would have gone red. Emitting both from one place
// means there is no second copy to lose.
//
// Two modes, because the two questions are asked at different times:
//
//	relnotes .                     every section in the changelog is well formed
//	relnotes -tag v1.0.0 ... .     the notes for one tag, or a non zero exit
//
// The first runs on every pull request, so a section that is added empty fails
// long before the tag. An empty section is the failure worth naming separately:
// a missing one is obvious the moment somebody looks, and an empty one reads as
// done in the diff and publishes a release with a heading and nothing under it.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// A section heading, as `## v1.0.0`. Anchored, because a `## v1.0.0 and later`
// somewhere in prose is not a section and matching it would split the file in
// a place nobody meant.
var heading = regexp.MustCompile(`^##\s+(v[0-9]+\.[0-9]+\.[0-9]+[0-9A-Za-z.\-+]*)\s*$`)

func main() {
	root := flag.String("root", ".", "repository root")
	changelog := flag.String("changelog", "CHANGELOG.md", "the changelog, relative to root")
	tag := flag.String("tag", "", "the tag being released; empty checks every section instead")
	repo := flag.String("repo", "", "owner/name, for the certificate identity in the preamble")
	ref := flag.String("ref", "", "the full git ref, as refs/tags/v1.0.0, for that same identity")
	out := flag.String("out", "", "write the notes here; empty writes to standard output")
	flag.Parse()
	if args := flag.Args(); len(args) > 0 {
		*root = args[0]
	}

	path := filepath.Join(*root, *changelog)
	source, err := os.ReadFile(path)
	if err != nil {
		fail("reading %s: %v", *changelog, err)
	}

	sections, err := parse(string(source))
	if err != nil {
		fail("%s: %v", *changelog, err)
	}
	if len(sections) == 0 {
		// No sections at all is a changelog that has stopped being a changelog,
		// or a heading format this command no longer recognises. Either way it
		// is a failure rather than a pass over an empty list, for the same
		// reason ldcheck refuses a build that sets no -X flag at all.
		fail("%s has no `## vX.Y.Z` section. Either nothing has been released or "+
			"the headings changed and this command is now reading nothing", *changelog)
	}

	if *tag == "" {
		checkAll(*changelog, sections)
		return
	}

	body, ok := sections[*tag]
	if !ok {
		fail("%s has no section for %s.\n"+
			"  Add `## %s` to it, with what changed under it, before pushing the tag.\n"+
			"  The alternative is a release whose notes are the generated pull request list.",
			*changelog, *tag, *tag)
	}
	if strings.TrimSpace(body) == "" {
		fail("the %s section of %s is empty.\n"+
			"  A heading with nothing under it publishes a release that says nothing, "+
			"and reads as finished in the diff.", *tag, *changelog)
	}
	if *repo == "" || *ref == "" {
		fail("-repo and -ref are both needed to write the verification preamble, and " +
			"notes without it tell somebody to trust a signature they are not shown how to check")
	}

	notes := preamble(*repo, *ref) + "\n" + strings.TrimSpace(body) + "\n"
	if *out == "" {
		fmt.Print(notes)
		return
	}
	if err := os.WriteFile(*out, []byte(notes), 0o600); err != nil {
		fail("writing %s: %v", *out, err)
	}
	fmt.Printf("relnotes: %s, %d bytes, preamble included\n", *tag, len(notes))
}

// checkAll is what runs on a pull request.
func checkAll(name string, sections map[string]string) {
	var empty []string
	for tag, body := range sections {
		if strings.TrimSpace(body) == "" {
			empty = append(empty, tag)
		}
	}
	if len(empty) > 0 {
		fail("these sections of %s have a heading and nothing under it: %s.\n"+
			"  A release cut against one of those publishes notes that say nothing.",
			name, strings.Join(empty, ", "))
	}
	fmt.Printf("relnotes: every section in %s has something under it (%d checked)\n",
		name, len(sections))
}

// parse splits the changelog into tag to body, refusing a repeated heading.
//
// Repeated rather than merged, because two `## v1.0.0` headings mean somebody
// wrote the second one not knowing the first existed, and merging them would
// publish half of what they wrote in an order neither of them chose.
func parse(source string) (map[string]string, error) {
	sections := map[string]string{}
	var current string
	var body strings.Builder

	flush := func() {
		if current != "" {
			sections[current] = body.String()
		}
		body.Reset()
	}

	scanner := bufio.NewScanner(strings.NewReader(source))
	// A release section runs to tens of kilobytes but a single line does not;
	// the default 64k line limit is fine and the explicit buffer says so.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	fenced := false
	for scanner.Scan() {
		line := scanner.Text()
		// A ``` fence can contain anything, including a line that looks like a
		// heading. Tracking the fence is what stops a code sample splitting the
		// file somewhere nobody meant.
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
		}
		if !fenced {
			if m := heading.FindStringSubmatch(line); m != nil {
				// Recorded before the check, not after. The section in hand is
				// only in the map once flush has run, so checking first meant a
				// repeat was never seen and the second heading quietly replaced
				// the first. Found by the test for it, which is the point of
				// writing one that has been watched to fail.
				flush()
				if _, seen := sections[m[1]]; seen {
					return nil, fmt.Errorf("two sections are headed %s", m[1])
				}
				current = m[1]
				continue
			}
		}
		if current != "" {
			body.WriteString(line)
			body.WriteByte('\n')
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	flush()
	return sections, nil
}

// preamble is the verification instructions, which ship with the thing they
// verify rather than in a page a person would have to know exists.
//
// A signature nobody knows how to check is decoration. The longer version,
// including how to rebuild these archives yourself, is at
// antifailure.dev/docs/security/releases.
func preamble(repo, ref string) string {
	identity := "https://github.com/" + repo + "/.github/workflows/release.yml@" + ref
	return `### Verifying this release

` + "`checksums.txt`" + ` names every archive by hash, and it is signed, so
checking the signature once covers all of them.

` + "```sh" + `
cosign verify-blob \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity "` + identity + `" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  checksums.txt

sha256sum --check --ignore-missing checksums.txt
` + "```" + `

There is no public key to fetch. The certificate inside the bundle records which
workflow, in which repository, at which tag produced these files, and
` + "`--certificate-identity`" + ` is what makes the check mean something: without it
you would be verifying that somebody signed this, rather than that we did.

` + "`sbom.spdx.json`" + ` lists what is inside the binaries, read out of the built
artifacts rather than from ` + "`go.mod`" + `. It is signed the same way, substituting
its own bundle.

These archives are reproducible. Building this tag again produces the same
bytes, so you can check the hash above against one you built yourself rather
than taking ours: antifailure.dev/docs/security/releases.

`
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "relnotes: "+format+"\n", args...)
	os.Exit(1)
}
