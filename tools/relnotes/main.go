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
//
// A section may also mark part of itself as detail, between a
// `<!-- relnotes:omit -->` line and a `<!-- relnotes:end -->` one. That part
// stays in CHANGELOG.md, where a changelog file is a reference document people
// search, and is replaced in the published notes by a pointer at the changelog
// on the site. It exists because a release note and a changelog file are read
// by different people for different reasons: v1.0.0's section ran to 66,831
// bytes, of which 45,183 were the per change entries under Added and Fixed,
// and the first thing somebody deciding whether to buy this reads should not
// be a catalogue.
//
// The gate grows with the feature rather than being loosened by it. An
// unbalanced marker fails, a second region in one section fails, an empty
// region fails, and the emptiness check reads what would be PUBLISHED rather
// than what is in the file, so wrapping a whole section and publishing a
// heading with a link under it fails exactly as an empty section does.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// A section heading, as `## v1.0.0`. Anchored, because a `## v1.0.0 and later`
// somewhere in prose is not a section and matching it would split the file in
// a place nobody meant.
var heading = regexp.MustCompile(`^##\s+(v[0-9]+\.[0-9]+\.[0-9]+[0-9A-Za-z.\-+]*)\s*$`)

// The two markers bounding the detail a release note points at instead of
// printing. An HTML comment, because it is invisible in every renderer that
// shows CHANGELOG.md, and a whole line, because half a line of prose that
// happens to contain the word should not move anything.
var (
	omitOpen  = regexp.MustCompile(`^<!--\s*relnotes:omit\s*-->$`)
	omitClose = regexp.MustCompile(`^<!--\s*relnotes:end\s*-->$`)
)

// Where the omitted detail went.
//
// The address is spelled out rather than linked as markdown, so it stays
// readable in a terminal reading the notes as text, which is where anybody
// scripting a release sees them first.
const pointer = `### The rest of this release, one entry per change

Every change has its own entry, grouped by what kind of change it is and
searchable: https://antifailure.dev/changelog

Each was written when the change was made, by whoever made it, and is dated by
the commit that landed it. The same entries are in CHANGELOG.md in this
repository.
`

// A release's section, split into what is published and what is pointed at.
type section struct {
	// The body with the omitted region replaced, in place, by the pointer.
	notes string
	// The body with the omitted region removed and nothing put in its place.
	// This is what the emptiness check reads, so a section that omits all of
	// itself fails rather than publishing a heading and a link.
	published string
	omitted   bool
}

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

	release, ok := sections[*tag]
	if !ok {
		fail("%s has no section for %s.\n"+
			"  Add `## %s` to it, with what changed under it, before pushing the tag.\n"+
			"  The alternative is a release whose notes are the generated pull request list.",
			*changelog, *tag, *tag)
	}
	if strings.TrimSpace(release.published) == "" {
		fail("the %s section of %s publishes nothing.\n"+
			"  A heading with nothing under it publishes a release that says nothing, "+
			"and reads as finished in the diff.\n"+
			"  A section every line of which sits inside `<!-- relnotes:omit -->` "+
			"publishes a heading and a link, which is the same release.", *tag, *changelog)
	}
	if *repo == "" || *ref == "" {
		fail("-repo and -ref are both needed to write the verification preamble, and " +
			"notes without it tell somebody to trust a signature they are not shown how to check")
	}

	notes := preamble(*repo, *ref) + "\n" + strings.TrimSpace(release.notes) + "\n"
	if *out == "" {
		fmt.Print(notes)
		return
	}
	if err := os.WriteFile(*out, []byte(notes), 0o600); err != nil {
		fail("writing %s: %v", *out, err)
	}
	where := "no detail omitted"
	if release.omitted {
		where = "detail pointed at the changelog"
	}
	fmt.Printf("relnotes: %s, %d bytes, preamble included, %s\n", *tag, len(notes), where)
}

// checkAll is what runs on a pull request.
//
// It reads what each section would PUBLISH rather than what it contains, so a
// section whose every line has been marked as detail fails here rather than at
// the tag. The markers themselves are checked in parse, which has already run
// by the time this is called; a malformed one never reaches this function.
func checkAll(name string, sections map[string]section) {
	var empty []string
	omitting := 0
	for tag, release := range sections {
		if strings.TrimSpace(release.published) == "" {
			empty = append(empty, tag)
		}
		if release.omitted {
			omitting++
		}
	}
	if len(empty) > 0 {
		sort.Strings(empty)
		fail("these sections of %s would publish nothing: %s.\n"+
			"  A release cut against one of those publishes notes that say nothing.",
			name, strings.Join(empty, ", "))
	}
	fmt.Printf("relnotes: every section in %s publishes something "+
		"(%d checked, %d pointing detail at the changelog)\n", name, len(sections), omitting)
}

// parse splits the changelog into tag to section, refusing a repeated heading.
//
// Repeated rather than merged, because two `## v1.0.0` headings mean somebody
// wrote the second one not knowing the first existed, and merging them would
// publish half of what they wrote in an order neither of them chose.
func parse(source string) (map[string]section, error) {
	sections := map[string]section{}
	var current string
	// notes and published are built side by side rather than one being derived
	// from the other, because the pointer has to land where the detail stood.
	// A release whose omitted region sits between two published parts would
	// otherwise print its link after the last of them, under the wrong
	// heading.
	var notes, published strings.Builder
	omitting, omitted := false, false
	openedAt := 0
	regionLines := 0
	line := 0

	fail := func(format string, args ...any) error {
		return fmt.Errorf("in the %s section, "+format, append([]any{current}, args...)...)
	}

	flush := func() error {
		if current != "" {
			if omitting {
				return fail("the `<!-- relnotes:omit -->` on line %d is never closed.\n"+
					"  Add `<!-- relnotes:end -->` where the detail ends, or take the open one out.\n"+
					"  Unclosed, it would drop the rest of the section from the published notes.",
					openedAt)
			}
			sections[current] = section{
				notes:     notes.String(),
				published: published.String(),
				omitted:   omitted,
			}
		}
		notes.Reset()
		published.Reset()
		omitting, omitted = false, false
		return nil
	}

	scanner := bufio.NewScanner(strings.NewReader(source))
	// A release section runs to tens of kilobytes but a single line does not;
	// the default 64k line limit is fine and the explicit buffer says so.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	fenced := false
	for scanner.Scan() {
		line++
		text := scanner.Text()
		trimmed := strings.TrimSpace(text)
		// A ``` fence can contain anything, including a line that looks like a
		// heading. Tracking the fence is what stops a code sample splitting the
		// file somewhere nobody meant, and it covers the markers for the same
		// reason: an example of one in a fence is documentation, not an
		// instruction.
		if strings.HasPrefix(trimmed, "```") {
			fenced = !fenced
		}
		if !fenced {
			if m := heading.FindStringSubmatch(text); m != nil {
				// Recorded before the check, not after. The section in hand is
				// only in the map once flush has run, so checking first meant a
				// repeat was never seen and the second heading quietly replaced
				// the first. Found by the test for it, which is the point of
				// writing one that has been watched to fail.
				if err := flush(); err != nil {
					return nil, err
				}
				if _, seen := sections[m[1]]; seen {
					return nil, fmt.Errorf("two sections are headed %s", m[1])
				}
				current = m[1]
				continue
			}
			if current != "" && omitOpen.MatchString(trimmed) {
				if omitting {
					return nil, fail("`<!-- relnotes:omit -->` on line %d opens a "+
						"region that is already open, from line %d.", line, openedAt)
				}
				if omitted {
					return nil, fail("there is a second `<!-- relnotes:omit -->` on line %d.\n"+
						"  One section points at the changelog once. Widen the first region "+
						"rather than opening another, or the notes carry the same link twice.",
						line)
				}
				omitting, openedAt, regionLines = true, line, 0
				notes.WriteString(pointer)
				continue
			}
			if current != "" && omitClose.MatchString(trimmed) {
				if !omitting {
					return nil, fail("`<!-- relnotes:end -->` on line %d closes nothing.\n"+
						"  Every one of them needs a `<!-- relnotes:omit -->` above it.", line)
				}
				if regionLines == 0 {
					return nil, fail("the region opened on line %d and closed on line %d is empty.\n"+
						"  An empty one publishes the pointer at the changelog with nothing "+
						"behind it, so take both markers out.", openedAt, line)
				}
				omitting, omitted = false, true
				continue
			}
		}
		if current == "" {
			continue
		}
		if omitting {
			if trimmed != "" {
				regionLines++
			}
			continue
		}
		notes.WriteString(text)
		notes.WriteByte('\n')
		published.WriteString(text)
		published.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if err := flush(); err != nil {
		return nil, err
	}
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
