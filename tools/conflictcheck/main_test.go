package main

import "testing"

// Each of these is one assertion in its own function rather than a table with
// stacked requires, because a helper that stops at the first failure hides
// every assertion after it, and an assertion that can never be seen going red
// is not an assertion.

func TestTheThreeMarkersGitWrites(t *testing.T) {
	for _, line := range []string{
		"<<<<<<< HEAD",
		"<<<<<<< ours",
		"||||||| merged common ancestors",
		">>>>>>> 406000d0 (enterprise: a way to ask, a way to read it, and a first operator)",
		"=======",
	} {
		if !isMarker(line) {
			t.Errorf("did not recognise a marker git writes: %q", line)
		}
	}
}

// The reason `=======` is matched exactly and the others by prefix. Every line
// here appears in this repository's own documentation, and calling any of them
// a conflict would make the gate one somebody turns off.
func TestPunctuationThatIsNotAConflict(t *testing.T) {
	for _, line := range []string{
		"=========",                      // a rule under a heading
		"======",                         // six, one short
		"| --- | --- |",                  // a table separator
		"<<<<<<<",                        // seven angle brackets and no space
		">>>>>>>",                        // the same, closing
		"    <<<<<<< HEAD",               // indented, so inside a code fence
		"Setext heading",                 //
		"=== a shell script section ===", //
	} {
		if isMarker(line) {
			t.Errorf("called ordinary text a conflict marker: %q", line)
		}
	}
}

// An exemption with no reason is refused. A row that says only a path cannot be
// told apart from somebody silencing a finding they did not understand.
func TestAnExemptionWithNoReasonIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/exemptions.tsv"
	if err := writeFile(path, "docs/merging.md\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := readExemptions(path); err == nil {
		t.Fatal("a row with no reason was accepted")
	}
}

func TestAnExemptionWhoseReasonIsOnlyWhitespaceIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/exemptions.tsv"
	if err := writeFile(path, "docs/merging.md\t   \n"); err != nil {
		t.Fatal(err)
	}
	if _, err := readExemptions(path); err == nil {
		t.Fatal("a row whose reason is whitespace was accepted")
	}
}

func TestAnExemptionWithAReasonIsRead(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/exemptions.tsv"
	body := "# a comment\n\ndocs/merging.md\tthis page teaches somebody to resolve one, so it quotes the markers\n"
	if err := writeFile(path, body); err != nil {
		t.Fatal(err)
	}
	got, err := readExemptions(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["docs/merging.md"] == "" {
		t.Fatalf("expected one row with a reason, got %v", got)
	}
}

// A missing file is not an error, and that is the honest state today: nothing
// in this repository needs exempting, so the file does not exist.
func TestAMissingExemptionFileIsNotAnError(t *testing.T) {
	got, err := readExemptions(t.TempDir() + "/absent.tsv")
	if err != nil {
		t.Fatalf("a missing exemption file was an error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no rows, got %v", got)
	}
}

func writeFile(path, body string) error {
	return osWriteFile(path, []byte(body), 0o644)
}
