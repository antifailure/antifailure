package installsh

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The repository install.sh is hard coded to install from. Held here so the
// stand in's routes are the routes the script asks for rather than routes
// chosen to match it.
const repo = "antifailure/antifailure"

// githubStandIn answers the requests install.sh makes of github.com.
//
// It replaced a `curl` that matched the tail of a URL, copied a file out of a
// directory and exited 22 when there was none, and the reason it had to go is
// the version lookup. That lookup reads an HTTP STATUS: 403 means the address
// has asked for too much, 404 means there is no such repository, a redirect to
// a tag is the answer, a redirect to the releases index means the repository has
// published nothing, and no answer at all means we could not ask. A stub whose
// only vocabulary is "exit 22" cannot say any of those, so a test written
// against one agrees with whatever the script already does. That is how the
// defect this package exists for survived: `AF_VERSION` was set in every
// session, so the resolution never ran, and nothing could have run it.
//
// So this serves real statuses over a real socket, and the curl on the session's
// PATH is the real curl with github.com rewritten to here. Every flag, header,
// redirect and status code the installer reads is genuine. The only thing that
// is not real is the address.
type githubStandIn struct {
	mu       sync.Mutex
	server   *httptest.Server
	fixtures string

	// tag is what /releases/latest redirects to. Empty means the repository has
	// published no release, which github.com answers with a redirect to the
	// releases index rather than with a 404: measured against
	// github.com/torvalds/linux/releases/latest on 2026-09-24, which answers
	// 302 to /torvalds/linux/releases. A 404 there means the repository itself
	// is absent or private, which is a different sentence.
	tag string

	// status, when non zero, is what /releases/latest answers instead of
	// redirecting. 403 is the rate limit, and the whole subject of this file.
	status int

	// denySuffix, when set, makes every path ending in it answer 403, so a
	// download that fails for a reason other than absence can be arranged.
	denySuffix string

	// asked records every path requested, so a test can assert which host the
	// script chose to ask rather than trusting that it chose the new one.
	asked []string

	// agents records the User-Agent of every request, which is the only thing
	// that can say WHICH of the two implementations ran. A test that means to
	// exercise the wget half and quietly runs the curl half is a dead control,
	// and this package has already had one: see pointAt.
	agents []string
}

func newStandIn(t *testing.T, fixtures string) *githubStandIn {
	t.Helper()
	g := &githubStandIn{fixtures: fixtures, tag: version}
	g.server = httptest.NewServer(g)
	t.Cleanup(g.server.Close)
	return g
}

func (g *githubStandIn) base() string { return g.server.URL }

func (g *githubStandIn) set(mutate func(*githubStandIn)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	mutate(g)
}

func (g *githubStandIn) paths() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.asked...)
}

func (g *githubStandIn) agentsSeen() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.agents...)
}

func (g *githubStandIn) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	tag, status, deny := g.tag, g.status, g.denySuffix
	g.asked = append(g.asked, r.URL.Path)
	g.agents = append(g.agents, r.UserAgent())
	g.mu.Unlock()

	if deny != "" && strings.HasSuffix(r.URL.Path, deny) {
		http.Error(w, "rate limit exceeded", http.StatusForbidden)
		return
	}

	web := "/" + repo + "/releases/latest"
	// The rewritten api.github.com, which nothing in the fixed script asks for.
	// It is served anyway, because the proof that the fix does something is the
	// old script answering "no release was found" to these same cases, and the
	// old script asks here.
	api := "/api/repos/" + repo + "/releases/latest"

	switch {
	case r.URL.Path == web || r.URL.Path == api:
		if status != 0 {
			http.Error(w, http.StatusText(status), status)
			return
		}
		if tag == "" {
			if r.URL.Path == api {
				// No release and the API is asked: a 404, which is what
				// api.github.com answers for a repository with no releases.
				http.NotFound(w, r)
				return
			}
			http.Redirect(w, r, "/"+repo+"/releases", http.StatusFound)
			return
		}
		if r.URL.Path == api {
			fmt.Fprintf(w, "{\n  \"tag_name\": %q,\n  \"name\": %q\n}\n", tag, tag)
			return
		}
		http.Redirect(w, r, "/"+repo+"/releases/tag/"+tag, http.StatusFound)

	// The release page, which the download failure asks about to tell a release
	// with no build for this platform from a version nobody published.
	case strings.HasPrefix(r.URL.Path, "/"+repo+"/releases/tag/"):
		if tag != "" && path.Base(r.URL.Path) == tag {
			fmt.Fprintf(w, "<html><title>Release %s</title></html>\n", tag)
			return
		}
		http.NotFound(w, r)

	case strings.HasPrefix(r.URL.Path, "/"+repo+"/releases/download/"):
		http.ServeFile(w, r, filepath.Join(g.fixtures, path.Base(r.URL.Path)))

	default:
		http.NotFound(w, r)
	}
}

// deadAddress is an address nothing is listening on, for the case where the
// question could not be asked at all. A port is opened and closed rather than
// guessed, because a guessed port that something else happens to hold would
// make this test pass for the wrong reason.
func deadAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return "http://" + addr
}

// writeWrappers puts a curl and a wget on the session's PATH that are the real
// tools with one substitution applied to their arguments.
//
// Both are written every time even though install.sh prefers curl, so the wget
// branch can be reached by removing the curl wrapper rather than by building a
// different session. A wget that is only a stub would prove nothing about wget:
// the two implementations read a status out of completely different output, and
// that difference is exactly where a fix in one and not the other hides.
func writeWrappers(t *testing.T, dir, base string, only ...string) {
	t.Helper()
	wanted := func(name string) bool {
		if len(only) == 0 {
			return true
		}
		for _, o := range only {
			if o == name {
				return true
			}
		}
		return false
	}
	write := func(name, real string) {
		if !wanted(name) {
			return
		}
		script := "#!/bin/sh\n" +
			"# The real " + name + ", with github.com rewritten to the test server.\n" +
			"# Arguments are rotated through \"$@\" so quoting survives.\n" +
			"n=$#\n" +
			"i=0\n" +
			"while [ $i -lt $n ]; do\n" +
			"  a=$1; shift\n" +
			"  case \"$a\" in\n" +
			"    https://github.com/*) a=\"" + base + "/${a#https://github.com/}\" ;;\n" +
			"    https://api.github.com/*) a=\"" + base + "/api/${a#https://api.github.com/}\" ;;\n" +
			"  esac\n" +
			"  set -- \"$@\" \"$a\"\n" +
			"  i=$((i+1))\n" +
			"done\n" +
			"exec " + real + " \"$@\"\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write("curl", realTool(t, "curl"))
	if w, err := exec.LookPath("wget"); err == nil {
		write("wget", w)
	}
}

// realTool is the absolute path of a tool as this machine has it, resolved
// before any wrapper is on the PATH, so a wrapper cannot exec itself.
func realTool(t *testing.T, name string) string {
	t.Helper()
	p, err := exec.LookPath(name)
	if err != nil {
		t.Fatalf("%s is not on this machine, so the installer cannot be run at all: %v", name, err)
	}
	if strings.Contains(p, os.TempDir()) {
		t.Fatalf("%s resolved to %s, which is inside a temporary directory, so it is a wrapper and would exec itself", name, p)
	}
	return p
}

// pointAt rewrites the wrappers to send github.com somewhere else, which is how
// the case with no answer at all is arranged.
//
// It rewrites only the wrappers this session still has, and that is not a
// detail. It used to rewrite both, so calling it after onlyWget put the curl
// wrapper back and the test ran the curl half of the installer while claiming to
// run the wget half. It was found by a mutation: breaking the wget
// implementation left that test green.
func (s *session) pointAt(base string) {
	s.t.Helper()
	var keep []string
	for _, name := range []string{"curl", "wget"} {
		if _, err := os.Stat(filepath.Join(s.stubs, name)); err == nil {
			keep = append(keep, name)
		}
	}
	if len(keep) == 0 {
		s.t.Fatal("this session has no fetcher wrapper left, so the installer could not run at all")
	}
	writeWrappers(s.t, s.stubs, base, keep...)
}

// onlyWget removes the curl wrapper and the system curl, so the second half of
// install.sh's fetcher selection is the half under test.
func (s *session) onlyWget(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("wget"); err != nil {
		// The zshPath rule: a skip reads as a pass, and in CI this is the only
		// thing that runs the wget branch at all.
		if os.Getenv("CI") != "" {
			t.Fatalf("wget is not on PATH and CI is set, so this cannot be skipped: %v.\n"+
				"install.sh has a wget implementation of every download and of the\n"+
				"version lookup, and this is the only test that runs it.", err)
		}
		t.Skipf("wget is not installed here, so the wget half of the installer cannot be run: %v", err)
	}
	if err := os.Remove(filepath.Join(s.stubs, "curl")); err != nil {
		t.Fatal(err)
	}
	s.wgetOnly = true
	hide(t, s, "curl")
	if !onPathIn(s.path, "wget") {
		t.Fatal("wget is not reachable on the session PATH, so this test would prove nothing")
	}
}
