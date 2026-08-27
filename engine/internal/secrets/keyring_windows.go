//go:build windows

package secrets

import (
	"errors"
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// SystemKeyring is the Windows Credential Manager, reached through advapi32.
//
// Directly through the API rather than through a command, which is the opposite
// of what macOS and Linux do here, and the reason is that Windows has no
// command that can read a secret back. cmdkey writes and lists credentials and
// deliberately never prints a password, so a cmdkey implementation could store
// a value and could never resolve one, which is a source that is present and
// useless. PowerShell can do it only with a module that is not installed by
// default.
//
// Calling the API needs no cgo. Windows system calls in Go go through
// LazyDLL, which is the ordinary way the standard library itself reaches
// win32, so this still builds and runs under CGO_ENABLED=0 like the rest of the
// engine. NewLazySystemDLL rather than NewLazyDLL, because the plain form
// searches the working directory first and an advapi32.dll dropped beside the
// binary would then be loaded in preference to the real one.
//
// The credential is stored as CRED_TYPE_GENERIC under the target name
// "service:account", so an entry made here is the same entry the macOS and
// Linux implementations would make, addressed the same way. Persistence is
// LOCAL_MACHINE, which survives a logoff and stays on this machine; the
// alternative, ENTERPRISE, roams the credential to a domain profile, and a
// production database URL following somebody onto every machine they sign in to
// is not a property this should have by default.
type SystemKeyring struct{}

func newSystemKeyring() Keyring { return SystemKeyring{} }

var (
	advapi32       = windows.NewLazySystemDLL("advapi32.dll")
	procCredReadW  = advapi32.NewProc("CredReadW")
	procCredWriteW = advapi32.NewProc("CredWriteW")
	procCredFree   = advapi32.NewProc("CredFree")
	procCredDelete = advapi32.NewProc("CredDeleteW")
)

const (
	credTypeGeneric         = 1
	credPersistLocalMachine = 2
	// The error advapi32 reports for a target name that is not stored. Named
	// rather than inlined, because the whole chain depends on telling a miss
	// apart from a failure and a bare 1168 in a comparison is not readable.
	errorNotFound = windows.ERROR_NOT_FOUND
)

// credentialW mirrors the CREDENTIALW structure.
//
// Field order and types are the API's, not ours, and must not be tidied: the
// struct is written by advapi32 into memory this process reads back, so a
// reordered field or a differently sized one reads the wrong bytes rather than
// failing to compile. The padding Go inserts between CredentialBlobSize and
// CredentialBlob on a 64-bit build is the same padding the C compiler inserts,
// which is what makes the layout agree without an explicit pad field.
type credentialW struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

func target(service, name string) string { return service + ":" + name }

func (SystemKeyring) Get(service, name string) (string, error) {
	targetName, err := windows.UTF16PtrFromString(target(service, name))
	if err != nil {
		return "", err
	}

	var cred *credentialW
	ret, _, callErr := procCredReadW.Call(
		uintptr(unsafe.Pointer(targetName)),
		uintptr(credTypeGeneric),
		0,
		uintptr(unsafe.Pointer(&cred)),
	)
	if ret == 0 {
		if errors.Is(callErr, errorNotFound) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("the Windows Credential Manager refused to read %s: %w",
			target(service, name), callErr)
	}
	// Freed on every path out, including the error ones. advapi32 allocated
	// this buffer and it holds a plaintext secret; leaking it leaks both memory
	// and the credential, for as long as the process lives.
	defer procCredFree.Call(uintptr(unsafe.Pointer(cred)))

	if cred.CredentialBlobSize == 0 || cred.CredentialBlob == nil {
		// A credential that exists and holds nothing. Present and empty, which
		// the chain treats as an answer: somebody who deliberately stored an
		// empty value should not fall through to a lower priority source with
		// a stale one in it.
		return "", nil
	}
	blob := unsafe.Slice(cred.CredentialBlob, cred.CredentialBlobSize)
	return decodeUTF16(blob), nil
}

func (SystemKeyring) Set(service, name, value string) error {
	targetName, err := windows.UTF16PtrFromString(target(service, name))
	if err != nil {
		return err
	}
	// The account is stored as the user name, so the entry is legible in the
	// Credential Manager control panel rather than appearing as a target with
	// no owner.
	userName, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}

	// UTF-16 little endian, which is the convention every Windows credential
	// helper follows and what the control panel expects to display. UTF-8 bytes
	// would round trip through this code correctly and show as mojibake to
	// anybody who looked, and to any other tool that read it.
	blob := encodeUTF16(value)

	cred := credentialW{
		Type:               credTypeGeneric,
		TargetName:         targetName,
		Persist:            credPersistLocalMachine,
		CredentialBlobSize: uint32(len(blob)),
		UserName:           userName,
	}
	if len(blob) > 0 {
		cred.CredentialBlob = &blob[0]
	}

	ret, _, callErr := procCredWriteW.Call(uintptr(unsafe.Pointer(&cred)), 0)
	// Referenced after the call so that the garbage collector cannot move or
	// collect the backing array while advapi32 is reading through the pointer
	// stored in the struct.
	runtime.KeepAlive(blob)
	runtime.KeepAlive(targetName)
	runtime.KeepAlive(userName)
	if ret == 0 {
		return fmt.Errorf("the Windows Credential Manager refused to write %s: %w",
			target(service, name), callErr)
	}
	// CredWrite replaces an existing credential of the same type and target
	// rather than refusing, which is what 'af secret set' needs: without it a
	// name could be written once and never corrected.
	return nil
}

func (SystemKeyring) Delete(service, name string) error {
	targetName, err := windows.UTF16PtrFromString(target(service, name))
	if err != nil {
		return err
	}
	ret, _, callErr := procCredDelete.Call(
		uintptr(unsafe.Pointer(targetName)), uintptr(credTypeGeneric), 0)
	if ret == 0 {
		if errors.Is(callErr, errorNotFound) {
			// Already gone. The caller wanted it gone, which is the same rule
			// every teardown in this product follows.
			return nil
		}
		return fmt.Errorf("the Windows Credential Manager refused to delete %s: %w",
			target(service, name), callErr)
	}
	return nil
}

// encodeUTF16 renders a string as the little endian UTF-16 bytes the credential
// blob holds. No terminator: the blob is length delimited, and a trailing NUL
// would come back as a stray rune on the way out.
func encodeUTF16(s string) []byte {
	units := windows.StringToUTF16(s)
	// StringToUTF16 appends a terminating zero, which is right for a pointer
	// argument and wrong for a counted buffer.
	units = units[:len(units)-1]
	out := make([]byte, len(units)*2)
	for i, u := range units {
		out[i*2] = byte(u)
		out[i*2+1] = byte(u >> 8)
	}
	return out
}

// decodeUTF16 reads the blob back.
//
// An odd length cannot come from encodeUTF16 and can come from another tool
// that wrote UTF-8 into the same slot. The trailing byte is dropped rather than
// the read failing, because returning an error for a credential somebody else
// stored would make this unable to read a value a user put there by hand.
func decodeUTF16(b []byte) string {
	units := make([]uint16, len(b)/2)
	for i := range units {
		units[i] = uint16(b[i*2]) | uint16(b[i*2+1])<<8
	}
	return windows.UTF16ToString(units)
}
