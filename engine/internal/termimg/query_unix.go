//go:build !windows

package termimg

import (
	"os"
	"syscall"
	"time"
)

// readReply reads until the device attributes answer is complete or the
// deadline passes.
//
// The read has to be bounded, and the obvious way to bound it does not work
// here. os.File.SetReadDeadline refuses a terminal on macOS with "file type
// does not support deadline": the runtime poller does not take a character
// device, so the deadline is never armed and the call fails before a single
// byte is read. That was not a theory. Detection came back "the terminal could
// not be asked what it draws" on every macOS terminal, including the ones that
// draw pictures, and the only reason it was caught rather than shipped is that
// a Capability carries the reason it reached its verdict.
//
// So the fd is put in non-blocking mode and read directly, which works on any
// terminal on any unix, and a terminal that has nothing to say answers EAGAIN
// rather than blocking. A terminal that refuses to go non-blocking falls back
// to the deadline, and if neither works the error says so and the caller
// reports an unknown capability rather than guessing at one.
func readReply(in *os.File, fd int, deadline time.Time) (string, error) {
	if err := syscall.SetNonblock(fd, true); err != nil {
		return readWithDeadline(in, deadline)
	}
	defer func() { _ = syscall.SetNonblock(fd, false) }()

	var reply []byte
	buf := make([]byte, 1024)
	for time.Now().Before(deadline) {
		n, err := syscall.Read(fd, buf)
		if n > 0 {
			reply = append(reply, buf[:n]...)
			if daComplete(reply) {
				// The last question has been answered, so everything before it
				// has already arrived. Returning here rather than reading to the
				// deadline is what keeps startup instant.
				return string(reply), nil
			}
			continue
		}
		if err != nil && err != syscall.EAGAIN && err != syscall.EINTR {
			return string(reply), err
		}
		time.Sleep(pollInterval)
	}
	return string(reply), nil
}
