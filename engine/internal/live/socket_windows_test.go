//go:build windows

package live

import (
	"net"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func dialLive(addr string) (net.Conn, error) {
	timeout := 5 * time.Second
	return winio.DialPipe(addr, &timeout)
}

// unlistenable is the address of a pipe that already exists. On Windows that
// is the case that matters: a pipe somebody else created first under our name
// must refuse the listen, never be joined.
func unlistenable(t *testing.T) string {
	return listenForTest(t).Path()
}

func TestTheLiveEndpointIsANamedPipeNodeCanReach(t *testing.T) {
	// Node's net.connect maps a path on Windows to a named pipe and answers
	// anything else with EACCES, which is how every runner live test failed
	// on windows-latest before this. The runner hands the address to
	// net.connect unchanged, so it has to be a pipe name.
	srv := listenForTest(t)
	if !strings.HasPrefix(srv.Path(), `\\.\pipe\antifailure-live-`) {
		t.Fatalf("the live endpoint is %q, which Node on Windows cannot connect to", srv.Path())
	}
	other := listenForTest(t)
	if other.Path() == srv.Path() {
		t.Fatalf("two runs were given the same pipe %q", srv.Path())
	}
}

func TestTheLivePipeAdmitsOnlyTheUserRunningTheEngine(t *testing.T) {
	// The unix socket is protected by its 0700 directory. A pipe has no
	// directory, so its own DACL is the whole protection, and the default one
	// admits Everyone to read and the anonymous logon. Read the DACL the pipe
	// actually carries and require exactly one allow entry, for this user.
	srv := listenForTest(t)
	sd, err := windows.GetNamedSecurityInfo(srv.Path(), windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("reading the pipe's security descriptor: %v", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		t.Fatalf("the pipe has no DACL, which admits everyone: %v", err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatalf("token user: %v", err)
	}
	if dacl.AceCount != 1 {
		t.Fatalf("the pipe's DACL has %d entries; it must admit this user and nobody else", dacl.AceCount)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		t.Fatalf("reading the entry: %v", err)
	}
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
		t.Fatalf("the one entry is of type %d, not an allow", ace.Header.AceType)
	}
	sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if !sid.Equals(user.User.Sid) {
		t.Fatalf("the pipe admits %s, not the user running the engine, %s", sid, user.User.Sid)
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatalf("control: %v", err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("the DACL is not protected, so inherited entries could widen it")
	}
}
