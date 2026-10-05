// Package privatefs creates files and directories that only the current user
// can read, and checks whether an existing one is still like that.
//
// On Unix that is a mode, and the callers here used to pass 0600 and 0700
// straight to the os package. Windows ignores those bits: Go reports every file
// as 0666 or 0777, and what decides who can read a file is its access control
// list, which a new file inherits from its directory. So on Windows a 0600
// "only its owner can read it" file was exactly as readable as its folder, and
// a repository checked out under C:\ gives every signed in user read access to
// its folders by default. This package makes the claim true on both: a mode on
// Unix, and on Windows an access control list naming only the current user,
// set as the file is created rather than after, so there is no moment when the
// bytes are on disk under the folder's wider list.
package privatefs

import "errors"

// ErrExposed is returned by Check when somebody other than the current user,
// the system account or the administrators can read the path.
var ErrExposed = errors.New("readable by other users")
