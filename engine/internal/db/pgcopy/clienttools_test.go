package pgcopy

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Which of several installed clients is the answer depends on the question,
// and the two questions look alike enough that the wrong answer reads as
// right.
//
// A copy asks "what should I run against a server of version N", and the
// answer is the oldest client that still clears N: a machine with 15, 16 and
// 18 copying a 16 server should use 16. `af doctor` asks "what could this
// machine read at all", with nothing connected and so no bar to clear, and the
// answer is the newest. Feeding the first function a bar of zero answers the
// second question with the OLDEST install, which on the machine this was
// written on reported a ceiling of Postgres 17 while an 18 client sat on the
// PATH and copied an 18 source successfully.

func candidates(majors ...int) []toolCandidate {
	out := make([]toolCandidate, 0, len(majors))
	for _, m := range majors {
		out = append(out, toolCandidate{path: "/pg/" + itoa(m) + "/pg_dump", major: m})
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestNewest_IsTheCeilingWhateverTheOrderTheyWereFoundIn(t *testing.T) {
	tests := []struct {
		name string
		have []toolCandidate
		want int
	}{
		{"one install", candidates(17), 17},
		{"newest last", candidates(15, 16, 18), 18},
		{"newest first", candidates(18, 16, 15), 18},
		{"a binary that would not say its version", candidates(0, 16), 16},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, newest(tc.have).major)
		})
	}
}

func TestBestFor_TakesTheOldestThatStillClearsTheServer(t *testing.T) {
	have := candidates(15, 16, 18)
	require.Equal(t, 16, bestFor(have, 16).major,
		"a 16 server should be copied with the 16 client, not the newest for its own sake")
	require.Equal(t, 18, bestFor(have, 17).major)
	require.Equal(t, 18, bestFor(have, 99).major,
		"with nothing new enough it returns the newest, so the refusal names the real gap")
}

// The two questions disagree, which is the whole reason they are two
// functions. A ceiling asked for through bestFor with no bar names the oldest
// install on the machine.
func TestTheCeilingIsNotBestForWithNoBar(t *testing.T) {
	have := candidates(15, 16, 18)
	require.Equal(t, 15, bestFor(have, 0).major)
	require.Equal(t, 18, newest(have).major,
		"af doctor reports what this machine could read, which is never the oldest install")
}

// And ClientTools has to ask the right one of them.
//
// This is the assertion the two pure tests above cannot make. The first draft
// of this function asked bestFor with a bar of zero, which type checks, reads
// as sensible, and reported a ceiling of Postgres 17 on a machine that copied
// an 18 source successfully three commands later.
func TestClientTools_ReportsTheCeilingAndNotTheOldestInstall(t *testing.T) {
	installed := candidates(15, 16, 18)
	previous := lookupTools
	lookupTools = func(string) ([]toolCandidate, error) { return installed, nil }
	t.Cleanup(func() { lookupTools = previous })

	path, major, err := ClientTools()
	require.NoError(t, err)
	require.Equal(t, 18, major,
		"doctor would tell a project on Postgres 18 that this machine tops out at 15")
	require.Contains(t, path, "18")
}

// A machine with pg_dump and no pg_restore fails halfway through a copy, with
// the archive already written, so it is not a machine that has the tools.
func TestClientTools_NeedsBothProgramsAndNotOne(t *testing.T) {
	previous := lookupTools
	lookupTools = func(name string) ([]toolCandidate, error) {
		if name == "pg_restore" {
			return nil, errNoRestoreForTest
		}
		return candidates(18), nil
	}
	t.Cleanup(func() { lookupTools = previous })

	_, _, err := ClientTools()
	require.Error(t, err)
}

var errNoRestoreForTest = errTest("pg_restore is not installed anywhere this looked")

type errTest string

func (e errTest) Error() string { return string(e) }
