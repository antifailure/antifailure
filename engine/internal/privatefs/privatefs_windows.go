//go:build windows

package privatefs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// currentUser is the SID of the account this process runs as.
func currentUser() (*windows.SID, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("privatefs: read the current user: %w", err)
	}
	return u.User.Sid, nil
}

// ownerOnly is a security descriptor granting the current user full control and
// nobody else anything. Protected, so nothing is inherited from the folder it
// lands in; inheritable when it is for a directory, so what is created inside
// is private too without asking.
func ownerOnly(dir bool) (*windows.SecurityAttributes, error) {
	sid, err := currentUser()
	if err != nil {
		return nil, err
	}
	flags := ""
	if dir {
		flags = "OICI"
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;" + flags + ";FA;;;" + sid.String() + ")")
	if err != nil {
		return nil, fmt.Errorf("privatefs: build the access list: %w", err)
	}
	return &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: sd,
	}, nil
}

// MkdirAll creates dir and any missing parents. A directory this call creates
// gets an access list naming only the current user, inherited by everything
// created inside it. One that already exists is left as it is: changing who can
// read a folder somebody else made is not this function's decision.
func MkdirAll(dir string) error {
	if info, err := os.Stat(dir); err == nil {
		if !info.IsDir() {
			return &os.PathError{Op: "mkdir", Path: dir, Err: windows.ERROR_ALREADY_EXISTS}
		}
		return nil
	}
	if parent := filepath.Dir(dir); parent != dir {
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return err
		}
	}
	sa, err := ownerOnly(true)
	if err != nil {
		return err
	}
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	if err := windows.CreateDirectory(p, sa); err != nil {
		if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			// Somebody made it between the Stat and here, which is the same
			// outcome as finding it already made.
			return nil
		}
		return &os.PathError{Op: "mkdir", Path: dir, Err: err}
	}
	return nil
}

// create opens a new file whose access list names only the current user. The
// list is part of the creation, so the file is never on disk under its folder's
// wider list, not even empty.
func create(name string) (*os.File, error) {
	sa, err := ownerOnly(false)
	if err != nil {
		return nil, err
	}
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	// The same sharing os.OpenFile asks for, so a reader such as a second
	// process looking at who holds a lock is not refused while it is open.
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, sa, windows.CREATE_NEW,
		windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_EXISTS) || errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			return nil, &os.PathError{Op: "open", Path: name, Err: os.ErrExist}
		}
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	return os.NewFile(uintptr(h), name), nil
}

// WriteFile writes data to name. An existing file is removed first rather than
// truncated, because truncating keeps the existing access list and the point is
// the new one.
func WriteFile(name string, data []byte) error {
	if err := os.Remove(name); err != nil && !os.IsNotExist(err) {
		return err
	}
	f, err := create(name)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return err
}

// CreateExclusive creates name for writing, failing with an error satisfying
// os.IsExist if it is already there.
func CreateExclusive(name string) (*os.File, error) { return create(name) }

// Restrict replaces the access list of an existing file or directory with one
// naming only the current user.
func Restrict(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	sa, err := ownerOnly(info.IsDir())
	if err != nil {
		return err
	}
	dacl, _, err := sa.SecurityDescriptor.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil)
}

// readRights is any right that lets an account read a file's contents.
const readRights = windows.FILE_READ_DATA | windows.GENERIC_READ | windows.GENERIC_ALL

// Check reports whether anybody beyond the current user, the system account and
// the administrators can read path, naming the first account that can. Those
// two are left out for the same reason root is left out of a Unix mode: they
// can take any file on the machine whatever its list says.
func Check(path string) error {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if dacl == nil {
		return fmt.Errorf("%w: it has no access list, which on Windows means everybody", ErrExposed)
	}
	me, err := currentUser()
	if err != nil {
		return err
	}
	trusted := []*windows.SID{me}
	for _, kind := range []windows.WELL_KNOWN_SID_TYPE{windows.WinLocalSystemSid, windows.WinBuiltinAdministratorsSid} {
		sid, err := windows.CreateWellKnownSid(kind)
		if err != nil {
			return err
		}
		trusted = append(trusted, sid)
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return err
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE ||
			ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 ||
			ace.Mask&readRights == 0 {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		known := false
		for _, t := range trusted {
			if sid.Equals(t) {
				known = true
				break
			}
		}
		if !known {
			return fmt.Errorf("%w: %s can read it", ErrExposed, accountName(sid))
		}
	}
	return nil
}

func accountName(sid *windows.SID) string {
	account, domain, _, err := sid.LookupAccount("")
	if err != nil {
		return sid.String()
	}
	if domain == "" {
		return account
	}
	return domain + `\` + account
}

// RestrictCommand is what a person runs to do what Restrict does: remove the
// inherited entries and grant the signed in user alone.
func RestrictCommand(path string, dir bool) string {
	grant := "%USERNAME%:F"
	if dir {
		grant = "%USERNAME%:(OI)(CI)F"
	}
	return `icacls "` + strings.TrimRight(path, `\`) + `" /inheritance:r /grant:r ` + grant
}
