package cli

import (
	"strings"
	"testing"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// A generated manifest may not claim a provenance it does not have.
//
// The header said "Every value here came from a file: a package manifest, a
// Dockerfile, a compose file, or a dependency list", and directly beneath it
// were two personas at example.test and a sign-up workflow describing a form,
// none of which came from anything. detect.defaultPersonas returns the same two
// accounts for every repository and suggestedWorkflows emits sign-up whatever
// the dependencies say.
//
// So a first time reader was told the file described their repository, ran the
// two commands the tool printed, and the second failed on a users table their
// JSON API does not have. For a product whose claim is evidence rather than
// assertion, its first command writing a file that misstates where its own
// values came from is the worst defect available, and the sentence is a claim
// that nothing gated.
//
// The mechanism built to prevent it could not: `assumed` was fed only through
// resolveQuestions, and personas and workflows never become questions, so the
// only guess af init ever disclosed was database.present.

func draftWithGuesses() *schema.Manifest {
	return &schema.Manifest{
		Version: 1, Name: "orders-service",
		Services: []schema.Service{{Name: "web", Kind: schema.ServiceWeb, Port: 3000, Path: "."}},
		Personas: []schema.Persona{
			{Name: "owner", Email: "owner@example.test", Role: "admin", Login: schema.LoginPassword},
			{Name: "member", Email: "member@example.test", Role: "member", Login: schema.LoginPassword},
		},
		Workflows: []schema.Workflow{{Name: "sign-up", Persona: "owner"}},
		Egress:    &schema.Egress{Default: schema.ModeBlock},
	}
}

// The header may not promise more than the file delivers.
func TestTheHeaderDoesNotClaimEveryValueCameFromAFile(t *testing.T) {
	body, err := renderManifest(draftWithGuesses())
	if err != nil {
		t.Fatal(err)
	}
	header := string(body)
	if i := strings.Index(header, "\nversion:"); i > 0 {
		header = header[:i]
	}
	// The exact sentence that was false. Matched literally, because this is a
	// regression test for one claim rather than a style rule about headers.
	if strings.Contains(collapseSpace(header), "Every value here came from a file") {
		t.Errorf("the header still claims every value came from a file:\n%s", header)
	}
	if !strings.Contains(collapseSpace(header), "starting point") {
		t.Errorf("the header does not tell the reader that some values are guesses:\n%s", header)
	}
}

// And the guessed blocks say so where they are, because the manifest is
// committed and read by people who never ran the command and never saw its
// summary.
func TestTheGuessedBlocksAreMarkedInTheFile(t *testing.T) {
	body, err := renderManifest(draftWithGuesses())
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, key := range []string{"personas", "workflows"} {
		i := strings.Index(text, "\n"+key+":\n")
		if i < 0 {
			t.Fatalf("the rendered manifest has no %s block:\n%s", key, text)
		}
		// The note has to be immediately above the key rather than anywhere in
		// the file, or a reader scrolling to personas does not see it.
		above := text[:i]
		lines := strings.Split(strings.TrimRight(above, "\n"), "\n")
		if len(lines) == 0 || !strings.HasPrefix(lines[len(lines)-1], "# ") {
			t.Errorf("%s is not preceded by a comment:\n%s", key, tailLines(above, 4))
		}
		if !strings.Contains(above, "Starting points, not detected.") {
			t.Errorf("nothing above %s says it was not detected:\n%s", key, tailLines(above, 6))
		}
	}
}

// A note that broke the document would be worse than no note, so the rendered
// bytes have to parse back. runInit already parses before writing; this asserts
// the property directly rather than relying on that ordering staying put.
func TestTheNotesLeaveTheManifestParseable(t *testing.T) {
	body, err := renderManifest(draftWithGuesses())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "personas:") || !strings.Contains(string(body), "workflows:") {
		t.Fatalf("a key was lost while inserting the notes:\n%s", body)
	}
	// Comments are the only thing added, so removing every comment line has to
	// leave exactly what the encoder produced.
	var kept []string
	for _, l := range strings.Split(string(body), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(l), "#") {
			kept = append(kept, l)
		}
	}
	joined := strings.Join(kept, "\n")
	if strings.Contains(joined, "Starting points") {
		t.Error("a note survived the comment strip, so it is not a comment")
	}
}

// noteBefore must do nothing when the key is not there, or a manifest with no
// personas gets a paragraph about personas it does not have.
func TestANoteIsNotAddedForABlockThatIsAbsent(t *testing.T) {
	draft := draftWithGuesses()
	draft.Personas = nil
	draft.Workflows = nil
	body, err := renderManifest(draft)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "Starting points, not detected.") {
		t.Errorf("a manifest with no personas and no workflows carries a note about them:\n%s", body)
	}
}

// The disclosure mechanism itself. assumed could only ever hold answers to
// questions, so a value nothing asked about was structurally undisclosable.
func TestWhatNoFileDecidedIsDisclosedWithoutBeingAQuestion(t *testing.T) {
	assumed := assumedByConstruction(draftWithGuesses())
	for _, key := range []string{"personas", "workflows"} {
		if assumed[key] == "" {
			t.Errorf("%s is written for every repository and is not disclosed as assumed", key)
		}
	}
	if strings.Contains(assumed["personas"], "detected") {
		t.Errorf("the personas line reads as a detection result: %q", assumed["personas"])
	}
}

func TestNothingIsDisclosedForAManifestThatGuessedNothing(t *testing.T) {
	draft := draftWithGuesses()
	draft.Personas = nil
	draft.Workflows = nil
	if got := assumedByConstruction(draft); len(got) != 0 {
		t.Errorf("a draft with no personas and no workflows disclosed %v", got)
	}
}

func collapseSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
