package installps1

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The repository install.ps1 is hard coded to install from, held here so the
// stand in's routes are the routes the script asks for rather than routes
// chosen to match it.
const repo = "antifailure/antifailure"

// githubStandIn answers the requests install.ps1 makes of github.com, with real
// statuses over a real socket. The script is pointed at it through AF_GITHUB,
// which is the setting a mirror of github.com uses, so every request, redirect
// and status the installer reads is genuine and only the address is not.
//
// The routes are the ones tools/installsh serves to install.sh, because the two
// installers make the same promises about the same release and an answer that
// meant one thing to one of them and another thing to the other would be a
// second product.
type githubStandIn struct {
	mu       sync.Mutex
	server   *httptest.Server
	fixtures string

	// tag is what /releases/latest redirects to. Empty means the repository
	// has published nothing, which github.com answers with a redirect to the
	// releases index rather than a 404.
	tag string

	// status, when non zero, is what /releases/latest answers instead of
	// redirecting. 403 is the rate limit.
	status int

	// elsewhere, when set, is where /releases/latest is redirected instead of
	// to a tag: what a proxy or a sign-in portal answers with.
	elsewhere string

	// denySuffix makes every path ending in it answer 403.
	denySuffix string

	// missing names files under releases/download that answer 404 even though
	// the fixture directory holds them.
	missing map[string]bool

	asked []string
}

func newStandIn(t *testing.T, fixtures, tag string) *githubStandIn {
	t.Helper()
	g := &githubStandIn{fixtures: fixtures, tag: tag, missing: map[string]bool{}}
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

func (g *githubStandIn) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	tag, status, deny, elsewhere := g.tag, g.status, g.denySuffix, g.elsewhere
	missing := g.missing[path.Base(r.URL.Path)]
	g.asked = append(g.asked, r.URL.Path)
	g.mu.Unlock()

	if deny != "" && strings.HasSuffix(r.URL.Path, deny) {
		http.Error(w, "rate limit exceeded", http.StatusForbidden)
		return
	}

	switch {
	case r.URL.Path == "/"+repo+"/releases/latest":
		if status != 0 {
			http.Error(w, http.StatusText(status), status)
			return
		}
		if elsewhere != "" {
			http.Redirect(w, r, elsewhere, http.StatusFound)
			return
		}
		if tag == "" {
			http.Redirect(w, r, "/"+repo+"/releases", http.StatusFound)
			return
		}
		http.Redirect(w, r, "/"+repo+"/releases/tag/"+tag, http.StatusFound)

	case strings.HasPrefix(r.URL.Path, "/"+repo+"/releases/tag/"):
		if tag != "" && path.Base(r.URL.Path) == tag {
			_, _ = fmt.Fprintf(w, "<html><title>Release %s</title></html>\n", tag)
			return
		}
		http.NotFound(w, r)

	case strings.HasPrefix(r.URL.Path, "/"+repo+"/releases/latest/download/"):
		if tag == "" {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(g.fixtures, path.Base(r.URL.Path)))

	case strings.HasPrefix(r.URL.Path, "/"+repo+"/releases/download/"):
		// Only the release that exists has assets. A request for any other tag
		// is what a typo in AF_VERSION produces.
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"+repo+"/releases/download/"), "/")
		if len(parts) != 2 || parts[0] != tag || missing {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(g.fixtures, parts[1]))

	default:
		http.NotFound(w, r)
	}
}

// deadAddress is an address nothing is listening on. Opened and closed rather
// than guessed, so a port something else happens to hold cannot make a test
// pass for the wrong reason.
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
