package insights

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/stretchr/testify/require"
)

// frame appends p to w as one frame of Docker's multiplexed log stream: eight
// bytes of header, the stream at index 0 and the payload's length as a big
// endian number at index 4, then the payload.
//
// Written out by hand because the moby api module keeps only the reader,
// StdCopy, whose constants describe this layout. The writer lives in the
// daemon, which the engine does not import. demuxLogs reads these frames with
// StdCopy itself, so a header written wrong here fails both tests below rather
// than passing them.
func frame(w *bytes.Buffer, stream stdcopy.StdType, p string) {
	var header [8]byte
	header[0] = byte(stream)
	binary.BigEndian.PutUint32(header[4:], uint32(len(p)))
	w.Write(header[:])
	w.WriteString(p)
}

// A failing migration's report quotes the tool, and the quote has to be the
// tool's words and nothing else.
//
// The stream is built frame by frame in the layout the daemon writes, and the
// first line is exactly 49 bytes long on purpose: 49 is the character 1, so a
// reader that keeps the header bytes that happen to print puts a 1 in front of
// the tool's first word, which is what af insights printed.
func TestDemuxLogs_TheFrameHeaderIsNotPartOfTheToolsMessage(t *testing.T) {
	first := "rehearsal sees DATABASE_URL=set SECRET_KEY_BASE=\n"
	require.Len(t, first, 49)
	second := "SECRET_KEY_BASE is empty, the app cannot boot\n"

	var stream bytes.Buffer
	frame(&stream, stdcopy.Stdout, first)
	frame(&stream, stdcopy.Stderr, second)

	require.Equal(t, strings.TrimSpace(first+second), demuxLogs(&stream))
}

// And a message longer than one read arrives whole, up to the bound.
func TestDemuxLogs_AMessageLongerThanOneReadArrivesWhole(t *testing.T) {
	line := strings.Repeat("x", 199) + "\n"
	var stream bytes.Buffer
	for range 100 {
		frame(&stream, stdcopy.Stdout, line)
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
