package explore_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/explore"
)

// codeOf is the catalog code an error carries, or empty when it carries none.
func codeOf(err error) aferrors.Code {
	var coded *aferrors.Error
	if errors.As(err, &coded) {
		return coded.Code()
	}
	return ""
}

func TestParseViewport_TheNamedSizesAreTheOnesTheHelpStates(t *testing.T) {
	// The sizes are printed in the help, in the tool's schema and in the
	// documentation. A name that silently maps to some other size is a name
	// everybody reading those three is misled by.
	phone, err := explore.ParseViewport(" PHONE ")
	require.NoError(t, err)
	require.Equal(t, explore.Viewport{Name: "phone", Width: 390, Height: 844, Mobile: true}, phone)

	tablet, err := explore.ParseViewport("tablet")
	require.NoError(t, err)
	require.Equal(t, explore.Viewport{Name: "tablet", Width: 768, Height: 1024}, tablet)

	desktop, err := explore.ParseViewport("desktop")
	require.NoError(t, err)
	require.Equal(t, explore.Viewport{Name: "desktop", Width: 1440, Height: 900}, desktop)

	custom, err := explore.ParseViewport("1280x720")
	require.NoError(t, err)
	require.Equal(t, explore.Viewport{Name: "custom", Width: 1280, Height: 720}, custom)

	none, err := explore.ParseViewport("")
	require.NoError(t, err)
	require.Zero(t, none, "no viewport keeps the runner's default window")
}

func TestParseViewport_RefusesWhatIsNotASizeItCanOpen(t *testing.T) {
	for _, in := range []string{"banana", "1280", "12x", "x720", "319x800", "800x319", "3841x800", "1280x3841"} {
		_, err := explore.ParseViewport(in)
		require.Errorf(t, err, "%q must be refused", in)
		require.Equalf(t, aferrors.AFAGT023, codeOf(err), "%q", in)
	}
	// The bounds themselves are sizes, so an off by one at either end is caught.
	for _, in := range []string{"320x320", "3840x3840"} {
		_, err := explore.ParseViewport(in)
		require.NoErrorf(t, err, "%q is inside the bounds", in)
	}
}

func TestParseBudget_ABareNumberIsStepsAndAUnitIsTime(t *testing.T) {
	steps, err := explore.ParseBudget("8")
	require.NoError(t, err)
	require.Equal(t, explore.Budget{Steps: 8}, steps)

	minutes, err := explore.ParseBudget("5m")
	require.NoError(t, err)
	require.Equal(t, explore.Budget{Duration: 5 * time.Minute}, minutes)

	for _, in := range []string{"0", "1001", "-3", "5 parsecs", "0s", "7h"} {
		_, err := explore.ParseBudget(in)
		require.Errorf(t, err, "%q must be refused", in)
		require.Equalf(t, aferrors.AFAGT023, codeOf(err), "%q", in)
	}
}

func TestParseStartPath_OnlyAPathOnTheEnvironmentIsAccepted(t *testing.T) {
	// A start path that could carry a host would point the browser anywhere
	// the caller named, and the environment under test is the only place an
	// exploration may go.
	path, err := explore.ParseStartPath("/settings/billing?tab=plan")
	require.NoError(t, err)
	require.Equal(t, "/settings/billing?tab=plan", path)

	for _, in := range []string{
		"settings", "//evil.example/x", "https://evil.example/", "/a b", "/a#b",
	} {
		_, err := explore.ParseStartPath(in)
		require.Errorf(t, err, "%q must be refused", in)
		require.Equalf(t, aferrors.AFAGT023, codeOf(err), "%q", in)
	}
}

func TestResolve_AnUndeclaredPersonaIsRefusedNamingTheDeclaredOnes(t *testing.T) {
	_, err := explore.Steering{Persona: "admin"}.Resolve([]string{"viewer", "owner"})
	require.Error(t, err)
	require.Equal(t, aferrors.AFAGT022, codeOf(err))
	require.Contains(t, err.Error(), "owner, viewer",
		"the refusal names the personas that would have worked, sorted")

	_, err = explore.Steering{Persona: "admin"}.Resolve(nil)
	require.Equal(t, aferrors.AFAGT022, codeOf(err))
	require.Contains(t, err.Error(), "declares none")

	resolved, err := explore.Steering{Persona: " viewer "}.Resolve([]string{"owner", "viewer"})
	require.NoError(t, err)
	require.Equal(t, "viewer", resolved.Persona)
}

func TestResolve_AFocusIsASentenceAndNotAScript(t *testing.T) {
	resolved, err := explore.Steering{Focus: "  the plan\n  and the   invoices "}.Resolve(nil)
	require.NoError(t, err)
	require.Equal(t, "the plan and the invoices", resolved.Focus)

	_, err = explore.Steering{Focus: strings.Repeat("word ", 101)}.Resolve(nil)
	require.Error(t, err)
	require.Equal(t, aferrors.AFAGT023, codeOf(err))
}

func TestResolved_FlagsReplayTheSameSteeringThroughAShell(t *testing.T) {
	// A start path with a query string holds an ampersand and a focus is a
	// sentence, so a replay line that did not quote them would background half
	// the command or split the sentence into flags when somebody pastes it.
	resolved, err := explore.Steering{
		Persona: "viewer", StartPath: "/settings/billing?tab=plan&x=1",
		Viewport: "Phone", Budget: "8", Focus: "the plan's invoices",
	}.Resolve([]string{"viewer"})
	require.NoError(t, err)
	require.Equal(t,
		`--persona viewer --start '/settings/billing?tab=plan&x=1' --viewport phone `+
			`--budget 8 --focus 'the plan'\''s invoices'`,
		resolved.Flags())

	custom, err := explore.Steering{Viewport: "1280x720", Budget: "90s"}.Resolve(nil)
	require.NoError(t, err)
	require.Equal(t, "--viewport 1280x720 --budget 1m30s", custom.Flags())

	nothing, err := explore.Steering{}.Resolve(nil)
	require.NoError(t, err)
	require.Empty(t, nothing.Flags(), "an unsteered run replays with the manifest's defaults")
}

func TestExploration_SettingSaysHowTheRunWasPointed(t *testing.T) {
	x := explore.Exploration{
		Persona: "viewer", StartPath: "/settings",
		Viewport: explore.Viewport{Name: "phone", Width: 390, Height: 844, Mobile: true},
		Focus:    "invoices",
	}
	require.Equal(t, `as viewer from /settings on phone 390x844 focused on "invoices"`, x.Setting())

	signedOut := explore.Exploration{StartPath: "/", Viewport: explore.Viewport{Width: 1280, Height: 800}}
	require.Equal(t, "signed out from / on 1280x800", signedOut.Setting())
}

func TestCompile_APhoneRunSaysItsWorkflowRunsInTheDefaultWindow(t *testing.T) {
	x := explore.Exploration{Name: "upgrade", Goal: "Upgrade the plan."}
	x.Journey = []explore.Move{{Kind: "goto", URL: "http://127.0.0.1:1/settings"}}
	_, plain := explore.Compile(x, "")
	for _, n := range plain {
		require.NotContains(t, n, "default window", "an unsteered run needs no window note")
	}

	x.Viewport = explore.Viewport{Name: "phone", Width: 390, Height: 844, Mobile: true}
	_, notes := explore.Compile(x, "")
	require.Contains(t, strings.Join(notes, "\n"), "ran on phone 390x844")
}
