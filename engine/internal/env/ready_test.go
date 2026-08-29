package env

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/events"
)

// The environment.ready event is the one the control plane stores and the pull
// request comment renders, so its payload is an interface rather than a log
// line. These assert the one decision the payload makes.

func fieldMap(t *testing.T, fields []events.Field) map[string]any {
	t.Helper()
	out := map[string]any{}
	for _, f := range fields {
		out[f.Key] = f.Value
	}
	require.Len(t, out, len(fields), "two fields shared a key, so one was lost")
	return out
}

func TestReadyCarriesThePreviewURLWhenThereIsOne(t *testing.T) {
	got := fieldMap(t, readyFields(&Result{
		URL: "http://localhost:8080", Golden: "g-1",
	}, "local", 12.5))

	// Both keys, because two shipped consumers disagree about the name: the
	// dashboard reads `url` and the control plane reads `preview_url`. A merge
	// that kept one of them would have emptied the other in silence.
	require.Equal(t, "http://localhost:8080", got["preview_url"], "the control plane reads this")
	require.Equal(t, "http://localhost:8080", got["url"], "the dashboard reads this")
	require.Equal(t, "g-1", got["golden_version"])
	require.Equal(t, "local", got["runtime"])
	require.Equal(t, 12.5, got["seconds"])
}

// An environment whose manifest declares no web service has no URL, and
// Result.URL says so by being empty. The event must omit the key rather than
// carry an empty string: a consumer cannot tell "" from a field nobody set,
// and one of those means there is nothing to open while the other means
// something is broken.
func TestReadyOmitsThePreviewURLRatherThanSendingAnEmptyOne(t *testing.T) {
	fields := readyFields(&Result{URL: "", Golden: "g-1"}, "local", 1)
	got := fieldMap(t, fields)

	for _, key := range []string{"preview_url", "url"} {
		_, present := got[key]
		require.False(t, present,
			"%s was sent as an empty string, which a consumer will render as a broken link", key)
	}
	require.Equal(t, "g-1", got["golden_version"], "the rest of the payload still has to arrive")
	require.Equal(t, "local", got["runtime"])
}

// The runtime is a parameter rather than the constant it used to be. There is
// one runtime on this branch; a second is in review, and an event that names
// the wrong one is worse than an event that names none, because it is stored
// and believed.
func TestReadyNamesTheRuntimeItWasGiven(t *testing.T) {
	got := fieldMap(t, readyFields(&Result{Golden: "g-1"}, "kubernetes", 1))
	require.Equal(t, "kubernetes", got["runtime"])
}
