//go:build windows

package live

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// Address is a named pipe with an unguessable name. dir is unused: Windows has
// no unix socket that Node can reach (its net module maps a path on Windows to
// a named pipe and refuses anything else with EACCES), and a pipe lives in the
// pipe namespace rather than in a directory, so a directory's permissions
// cannot protect it. Its own security descriptor does, in listen.
func Address(string) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("naming the live view's pipe: %w", err)
	}
	return `\\.\pipe\antifailure-live-` + hex.EncodeToString(b), nil
}

// listen creates the pipe admitting only this user. Three properties carry the
// guarantee the unix socket's 0700 directory gives elsewhere:
//
//   - The DACL names one SID, the user running the engine, and is protected
//     from inheritance. The default pipe ACL would also admit Everyone to read
//     and the anonymous logon, so leaving it empty is not the safe default.
//   - go-winio creates the first instance with FILE_CREATE, so a pipe somebody
//     made first under this name refuses the listen rather than being joined.
//     With 128 random bits in the name that is a fault, not a race, and it is
//     reported rather than retried.
//   - go-winio sets FILE_PIPE_REJECT_REMOTE_CLIENTS, so the pipe is unreachable
//     over SMB, the one way a named pipe is reachable from another machine.
func listen(addr string) (net.Listener, error) {
	sddl, err := ownerOnly()
	if err != nil {
		return nil, err
	}
	return winio.ListenPipe(addr, &winio.PipeConfig{SecurityDescriptor: sddl})
}

// ownerOnly is a protected DACL granting full access to the current user and
// to nobody else.
func ownerOnly() (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("reading the user the live view's pipe belongs to: %w", err)
	}
	return "D:P(A;;GA;;;" + user.User.Sid.String() + ")", nil
}

// release has nothing to do: a pipe leaves nothing on disk, and it disappears
// with its last handle.
func release(string) {}
