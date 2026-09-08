// Package generated answers one question for the gates: was this Go file
// written by a person.
//
// It exists because of what happens when a generator packages CONTENT into Go.
// engine/internal/docs/pages.gen.go carries the documentation site, page by
// page, as string literals, so every address, every example email and every
// error code any page mentions arrives in Go source looking like something the
// engine itself says. Three gates read it that way on the day it landed:
// claimcheck called /docs/llms-full.txt a dead link, contactcheck called an
// example address in a guide a published contact route this project cannot
// answer, and errcheck said something returns AF-CPL-003 because the error
// reference lists it.
//
// None of those was a defect. Each claim is real prose, authored in a Markdown
// page, and each is already gated where it was authored. What was wrong was
// reading a copy as an original.
//
// So the rule is one sentence: a generated file's literals were authored
// somewhere else and are checked where they were authored. It lives here
// rather than three times over, because three copies of a rule that has to
// agree is how they stop agreeing.
package generated

import (
	"bytes"
	"os"
	"regexp"
)

// marker is the convention every generator in this repository follows, and the
// one the Go toolchain itself recognises.
var marker = regexp.MustCompile(`(?m)^// Code generated .* DO NOT EDIT\.$`)

// headBytes is how much of a file is read looking for the marker.
//
// The marker has to be before the package clause, and no file in this
// repository carries a licence header anywhere near this long, so a file whose
// first four kilobytes hold no package clause is not one this is deciding
// about.
const headBytes = 4 << 10

// Is reports whether the file at path declares itself generated.
//
// A file that cannot be read is reported as not generated, so the caller
// carries on and checks it rather than skipping it on an error. Failing open
// here would mean an unreadable file quietly exempting itself from every gate
// that asks.
func Is(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()

	head := make([]byte, headBytes)
	n, _ := f.Read(head)
	return IsSource(head[:n])
}

// IsSource is Is over bytes already in hand.
func IsSource(src []byte) bool {
	// Only before the package clause. A file that merely QUOTES the sentence,
	// such as the generator that writes it, is still somebody's own code and
	// is still checked.
	//
	// The clause is looked for at the start of the file as well as after a
	// newline. Without the first case a file beginning "package p" has no
	// clause this can find, the whole file is searched, and a hand written
	// file quoting the sentence anywhere in it excuses itself from every gate
	// that asks. That is the exact hole this function exists to close, and it
	// was open for one commit.
	if bytes.HasPrefix(src, []byte("package ")) {
		return false
	}
	if at := bytes.Index(src, []byte("\npackage ")); at >= 0 {
		src = src[:at]
	}
	return marker.Match(src)
}
