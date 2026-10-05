package termimg

import (
	"errors"
	"os"
)

// errNoQueryOnWindows is why no question is asked on Windows.
var errNoQueryOnWindows = errors.New("a Windows console takes no read deadline, so its answer could not be " +
	"waited for without the risk of reading the keys typed after it; set AF_IMAGES to sixel, kitty, " +
	"iterm2 or off to say what this terminal draws")

// Query asks nothing on Windows, and says why.
//
// The read has to be bounded, and Windows offers no way to bound it that is
// safe here. os.File.SetReadDeadline refuses a console handle and a pipe alike
// with "file type does not support deadline", which this package believed was
// a macOS problem only; on Windows it meant the questions were written, the
// read failed at once, and the terminal's answers were left in the input
// buffer for the dashboard to read as keystrokes. A reader that outlives the
// deadline would take the person's first real keys instead. So the questions
// are not written, Detect reports an unknown capability with this reason, and
// AF_IMAGES is the way to say what the terminal draws.
func Query(_, _ *os.File) (string, error) {
	return "", errNoQueryOnWindows
}
