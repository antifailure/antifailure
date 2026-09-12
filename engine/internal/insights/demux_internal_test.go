package insights

import (
	"bytes"
	"strings"
	"testing"

	"github.com/docker/docker/pkg/stdcopy"
	"github.com/stretchr/testify/require"
)

// A failing migration's report quotes the tool, and the quote has to be the
// tool's words and nothing else.
//
// The stream is built the way Docker builds it, with a real frame writer, and
// the first line is exactly 49 bytes long on purpose: 49 is the character 1,
// so a reader that keeps the header bytes that happen to print puts a 1 in
// front of the tool's first word, which is what af insights printed.
func TestDemuxLogs_TheFrameHeaderIsNotPartOfTheToolsMessage(t *testing.T) {
	first := "rehearsal sees DATABASE_URL=set SECRET_KEY_BASE=\n"
	require.Len(t, first, 49)
	second := "SECRET_KEY_BASE is empty, the app cannot boot\n"

	var stream bytes.Buffer
	_, err := stdcopy.NewStdWriter(&stream, stdcopy.Stdout).Write([]byte(first))
	require.NoError(t, err)
	_, err = stdcopy.NewStdWriter(&stream, stdcopy.Stderr).Write([]byte(second))
	require.NoError(t, err)

	require.Equal(t, strings.TrimSpace(first+second), demuxLogs(&stream))
}

// And a message longer than one read arrives whole, up to the bound.
func TestDemuxLogs_AMessageLongerThanOneReadArrivesWhole(t *testing.T) {
	line := strings.Repeat("x", 199) + "\n"
	var stream bytes.Buffer
	w := stdcopy.NewStdWriter(&stream, stdcopy.Stdout)
	for range 100 {
		_, err := w.Write([]byte(line))
		require.NoError(t, err)
	}
	require.Equal(t, strings.TrimSpace(strings.Repeat(line, 100)), demuxLogs(iotestOneByteReader{&stream}))
}

// iotestOneByteReader hands back one byte per Read, which is the least a
// reader is allowed to return and the case a single Read cannot survive.
type iotestOneByteReader struct{ r *bytes.Buffer }

func (o iotestOneByteReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return o.r.Read(p[:1])
}
