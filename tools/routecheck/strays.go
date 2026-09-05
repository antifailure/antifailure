package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Stray is one place in www that builds a control plane URL by hand.
type Stray struct {
	File string
	Line int
	Text string
}

// FindStrayCallSites reports every line of www source that names
// CONTROL_PLANE_URL outside the file that defines it and the file that holds
// the inventory.
//
// WHY THIS AND NOT AN EXTRACTOR. The call sites used to read
//
//	fetch(`${CONTROL_PLANE_URL}/v1/applications`, { ... })
//
// and the first design here was to pull the paths back out of those template
// literals. That cannot be made honest. A template literal can hold an
// expression, a path can arrive in a variable, and a route can be assembled a
// segment at a time; an extractor that reads four call sites and cannot read
// the fifth has to either guess or say nothing, and saying nothing is how a
// gate reports a clean run over the one call it did not understand.
//
// So the rule is inverted. The site is not allowed to build a control plane URL
// anywhere except the inventory, and this refuses one that does. There is no
// call site this can fail to see, because the failure mode of not seeing one is
// now a failure rather than a silence. The paths themselves are then plain
// string literals in one file, which needs no cleverness to read.
//
// Comments are stripped first. A rule that tripped on the word in a sentence is
// a rule people route around by rewording the sentence.
func FindStrayCallSites(wwwRoot string) ([]Stray, error) {
	var strays []Stray
	err := filepath.WalkDir(wwwRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", ".next", "out", ".git":
				return filepath.SkipDir
			case "test":
				// The suite, which is not bundled into the site: next.config.ts
				// exports from app/, components/ and lib/, and nothing under
				// test/ reaches a browser. It is skipped because the test that
				// pins these URLs has to import CONTROL_PLANE_URL to assert
				// that controlPlaneUrl() still produces the same string the
				// call sites used to build by hand, and a rule that refused
				// that would forbid the one check proving the refactor changed
				// nothing. A call site cannot hide here: this directory ships
				// nowhere.
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(path) {
		case ".ts", ".tsx", ".js", ".jsx", ".mjs":
		default:
			return nil
		}
		rel, relErr := filepath.Rel(wwwRoot, path)
		if relErr != nil {
			rel = path
		}
		if rel == definitionFile || rel == inventoryFile {
			return nil
		}
		f, openErr := os.Open(path)
		if openErr != nil {
			return openErr
		}
		defer f.Close()

		scan := bufio.NewScanner(f)
		scan.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		line, st := 0, scanState{}
		for scan.Scan() {
			line++
			code, next := stripComments(scan.Text(), st)
			st = next
			if strings.Contains(code, "CONTROL_PLANE_URL") {
				strays = append(strays, Stray{File: filepath.Join("www", rel), Line: line, Text: scan.Text()})
			}
		}
		return scan.Err()
	})
	return strays, err
}

// scanState is what a line of source leaves the scanner in. A block comment
// and a template literal both survive a newline, so both have to be carried to
// the next line or a `CONTROL_PLANE_URL` on the line after either one is read
// in the wrong mode.
type scanState struct {
	inComment  bool
	inTemplate bool
}

// stripComments removes // and /* */ comments from one line, given the state
// the previous line ended in, and reports the state this one ends in.
//
// IT IS STRING AWARE, AND THAT IS NOT A REFINEMENT. The first version was not,
// and it had a silent hole big enough to drive the whole failure through:
//
//	const u = "https://antifailure.dev" + CONTROL_PLANE_URL;
//
// The `//` inside `https://` read as the start of a comment, the rest of the
// line was discarded, and the identifier vanished. A call site written that way
// passed the gate without a word, which is precisely the defect this command
// exists to stop: a check that cannot say no is worse than no check.
// TestStripCommentsDoesNotLoseCodeAfterAUrlInAString is the instrument for it.
//
// String CONTENTS are kept rather than blanked, deliberately. The identifier in
//
//	fetch(`${CONTROL_PLANE_URL}/v1/applications`)
//
// is inside a template literal and is real code, so blanking literals would
// reintroduce the same hole from the other direction. The cost is that the
// characters `CONTROL_PLANE_URL` written inside an ordinary string would be
// reported as a call site. That is a false positive, which is loud and gets
// fixed in a minute, rather than a false negative, which is silent forever.
//
// It is not a JavaScript parser and does not need to be: it is only ever asked
// whether an identifier survives. The one construct it does not model is a
// regular expression literal, because to hide the identifier there a line would
// have to open a regex containing `//`, and `//` does not open a regex.
func stripComments(line string, st scanState) (string, scanState) {
	var out strings.Builder
	quote := byte(0)
	if st.inTemplate {
		quote = '`'
	}
	for i := 0; i < len(line); i++ {
		c := line[i]
		if st.inComment {
			if strings.HasPrefix(line[i:], "*/") {
				st.inComment = false
				i++
			}
			continue
		}
		if quote != 0 {
			// Inside a string. Nothing here opens a comment, and the contents
			// are kept because `${CONTROL_PLANE_URL}` is code.
			out.WriteByte(c)
			if c == '\\' && i+1 < len(line) {
				i++
				out.WriteByte(line[i])
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' || c == '`' {
			quote = c
			out.WriteByte(c)
			continue
		}
		if strings.HasPrefix(line[i:], "//") {
			break
		}
		if strings.HasPrefix(line[i:], "/*") {
			st.inComment = true
			i++
			continue
		}
		out.WriteByte(c)
	}
	// A `"` or `'` left open at the end of a line is a syntax error rather than
	// a continuation, so only a template literal carries over.
	st.inTemplate = quote == '`'
	return out.String(), st
}
