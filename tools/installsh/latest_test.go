package installsh

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Resolving "latest", which is what every advertised install does.
//
// THE DEFECT. `curl -fsSL https://antifailure.dev/install.sh | sh` read
// api.github.com/repos/antifailure/antifailure/releases/latest to find out which
// release to install. That endpoint allows an unauthenticated caller sixty
// requests an hour PER IP ADDRESS and answers 403 once they are spent. `curl -f`
// prints nothing and exits non zero on a 403, the `sed | head` pipeline threw
// the exit status away, and the empty version fell into
//
//	antifailure: no release was found; set AF_VERSION to install a specific one
//
// which is a statement about the product, made in the one situation where
// nothing whatsoever had been established about the product. A customer behind a
// corporate NAT, in a cloud network or on a shared CI runner shares that budget
// with every other caller on the address. Our own CI reached it on #576, where
// the job that installs the way a customer's workflow does invokes the installer
// eleven times and the first one was told there are no releases.
//
// WHY THERE WAS NO TEST. Every session in this package set AF_VERSION, so the
// branch that resolves "latest" never executed, and the stub curl could not have
// expressed a 403 if it had. Both of those are fixed here: `asked` is empty in
// these tests, and the stand in answers real statuses over a real socket.
//
// WHAT EACH ARM IS FOR. Four answers used to be reported as one sentence:
// "I could not ask", "there is no such repository", "the repository has
// published no release" and "the release has no build for this platform" all
// arrived as "no release was found" or "could not download". Every test below
// asserts the sentence it should get AND the absence of the sentence it used to
// get, because a message that merely adds words to the old lie is not a fix.

// theOldLie is the sentence the fixed script must never print. It is matched on
// its distinctive half rather than in full, so a rewording of the surrounding
// text cannot make this assertion stop looking.
const theOldLie = "no release was found"

// refusesToResolve runs an install with no AF_VERSION and asserts it failed
// closed, said the right thing, and did not say the old thing.
func refusesToResolve(t *testing.T, s *session, want []string, unwanted []string) string {
	t.Helper()
	out, err := s.run()
	if err == nil {
		t.Fatalf("the installer succeeded with no release to install:\n%s", out)
	}
	for _, w := range want {
		contains(t, out, w)
	}
	for _, u := range append(unwanted, theOldLie) {
		absent(t, out, u)
	}
	if _, statErr := os.Stat(filepath.Join(s.binDir(), "af")); statErr == nil {
		t.Error("af was installed although no version was ever resolved")
	}
	if _, statErr := os.Stat(filepath.Join(s.home, ".zshrc")); statErr == nil {
		t.Error("a refused install still edited the profile")
	}
	return out
}

// The working path first, because every refusal below is only interesting if
// this one succeeds: an installer that refused everything would pass all of them.
func TestLatestIsResolvedFromTheRedirectAndInstalled(t *testing.T) {
	s := newSession(t)
	s.asked = ""
	out := s.install()

	contains(t, out, "Downloading "+name())
	contains(t, out, "Checksum verified")
	contains(t, out, "Installed "+version)
	if _, err := os.Stat(filepath.Join(s.binDir(), "af")); err != nil {
		t.Fatalf("af was not installed: %v", err)
	}
}

// The lookup must not go near api.github.com, and the way to know is to watch
// which routes were asked rather than to read the script. This is the regression
// guard: a future edit that reaches for the API again reintroduces a rate limit
// that only shows up on somebody else's network.
func TestTheLookupNeverAsksTheRateLimitedApi(t *testing.T) {
	s := newSession(t)
	s.asked = ""
	s.install()

	paths := s.github.paths()
	if len(paths) == 0 {
		t.Fatal("the stand in was never asked anything, so this test proves nothing")
	}
	saw := false
	for _, p := range paths {
		if strings.HasPrefix(p, "/api/") {
			t.Errorf("the installer asked api.github.com for %s, which is rate limited to sixty requests an hour per address", p)
		}
		if p == "/"+repo+"/releases/latest" {
			saw = true
		}
	}
	if !saw {
		t.Errorf("nothing asked github.com/%s/releases/latest, so the version came from somewhere this test does not know about: %v", repo, paths)
	}
}

// The arm this whole change exists for.
func TestARateLimitedLookupSaysSoRatherThanDenyingTheRelease(t *testing.T) {
	for _, status := range []int{403, 429} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			s := newSession(t)
			s.asked = ""
			s.github.set(func(g *githubStandIn) { g.status = status })

			out := refusesToResolve(t, s,
				[]string{strconv.Itoa(status), "asked for too much", "AF_VERSION"},
				// The two facts it must not assert, because it established
				// neither: that the repository has no release, and that there
				// is no repository.
				[]string{"published no release", "there is no " + repo})
			// The remedy has to be in the message, because it is the only way
			// through for somebody who needs the install now.
			contains(t, out, "https://github.com/"+repo+"/releases")
		})
	}
}

// A repository that has published nothing is the one case where "there is no
// release" is true, and it has to be said only here. github.com answers it with
// a redirect to the releases index, not with a 404.
func TestARepositoryWithNoReleaseSaysThatAndNothingElse(t *testing.T) {
	s := newSession(t)
	s.asked = ""
	s.github.set(func(g *githubStandIn) { g.tag = "" })

	refusesToResolve(t, s,
		[]string{"published no release", "AF_VERSION"},
		[]string{"asked for too much", "nothing answered"})
}

// No answer at all: a dropped network, a DNS failure, a firewall that blackholes
// the connection. The old script could not tell this from a rate limit and
// neither could the reader.
func TestAGithubNothingAnswersForSaysItCouldNotAsk(t *testing.T) {
	s := newSession(t)
	s.asked = ""
	s.pointAt(deadAddress(t))

	refusesToResolve(t, s,
		[]string{"nothing answered at", "can reach github.com", "AF_VERSION"},
		[]string{"published no release", "asked for too much"})
}

// A 404 on the releases page is the repository being absent or private, which is
// neither of the two above.
func TestA404SaysThereIsNoSuchRepository(t *testing.T) {
	s := newSession(t)
	s.asked = ""
	s.github.set(func(g *githubStandIn) { g.status = 404 })

	refusesToResolve(t, s,
		[]string{"404", "there is no " + repo},
		[]string{"published no release", "asked for too much", "nothing answered"})
}

// A redirect is the one part of this exchange the far end chooses, so the tag it
// names is input rather than a version. A segment that is not tag shaped is
// refused rather than pasted into the download URL this script composes next.
func TestARedirectToSomethingThatIsNotATagIsRefused(t *testing.T) {
	for _, tag := range []string{"v9.9.9%2fetc", "v9.9.9?x=1", "v9.9.9 oops"} {
		t.Run(tag, func(t *testing.T) {
			s := newSession(t)
			s.asked = ""
			s.github.set(func(g *githubStandIn) { g.tag = tag })

			out := refusesToResolve(t, s,
				[]string{"not a release tag"},
				[]string{"published no release"})
			// The refused text is not echoed. A redirect carrying terminal
			// escapes would otherwise be printed to a terminal by an installer
			// running as the reader.
			absent(t, out, tag)
		})
	}
}

// The wget half. install.sh implements every download and the version lookup
// twice, once per tool, and the two read a status out of completely different
// output: curl is asked for it with -w, wget only ever writes it into its own
// log. A fix in one and not the other is where this defect would survive, so
// both the working path and a refusal run through wget here.
func TestTheWgetImplementationResolvesLatestAndReportsARateLimit(t *testing.T) {
	t.Run("resolves", func(t *testing.T) {
		s := newSession(t)
		s.asked = ""
		s.onlyWget(t)
		out := s.install()
		contains(t, out, "Installed "+version)
	})

	t.Run("rate limited", func(t *testing.T) {
		s := newSession(t)
		s.asked = ""
		s.onlyWget(t)
		s.github.set(func(g *githubStandIn) { g.status = 403 })
		refusesToResolve(t, s,
			[]string{"403", "asked for too much"},
			[]string{"published no release"})
	})

	t.Run("nothing answers", func(t *testing.T) {
		s := newSession(t)
		s.asked = ""
		s.onlyWget(t)
		s.pointAt(deadAddress(t))
		refusesToResolve(t, s,
			[]string{"nothing answered at"},
			[]string{"published no release"})
	})
}

// The third answer that used to be collapsed: a release that exists and carries
// no build for this platform. "could not download" was said to that, to a
// timeout and to a proxy alike.
func TestAReleaseWithNoBuildForThisPlatformSaysThat(t *testing.T) {
	s := newSession(t)
	if err := os.Remove(filepath.Join(s.fixtures, name()+".tar.gz")); err != nil {
		t.Fatal(err)
	}
	out, err := s.run()
	if err == nil {
		t.Fatalf("the installer succeeded with no archive to install:\n%s", out)
	}
	contains(t, out, "404")
	contains(t, out, "does not include the build for ")
	contains(t, out, "https://github.com/"+repo+"/releases/tag/"+version)
	absent(t, out, "could not download")
	absent(t, out, "nothing answered")
}

// And the same download failing for a reason that is not absence. This is the
// arm that proves the classification is being read from the server rather than
// assumed: the file is right there, and the answer is still no.
func TestADownloadRefusedByARateLimitIsNotReportedAsAMissingBuild(t *testing.T) {
	s := newSession(t)
	s.github.set(func(g *githubStandIn) { g.denySuffix = name() + ".tar.gz" })

	out, err := s.run()
	if err == nil {
		t.Fatalf("the installer succeeded with no archive to install:\n%s", out)
	}
	contains(t, out, "403")
	contains(t, out, "asked for too much")
	absent(t, out, "does not include")
	absent(t, out, "could not download")
}

// checksums.txt carried the same defect in its own words: "no checksums.txt was
// published for $VERSION" was printed for a network that dropped it. It still
// refuses either way, which is the property the block above exists for, but only
// one of the two is worth running again and the reader could not tell which.
func TestAChecksumsFileRefusedByARateLimitIsNotReportedAsUnpublished(t *testing.T) {
	s := newSession(t)
	s.github.set(func(g *githubStandIn) { g.denySuffix = "checksums.txt" })

	out := refuses(t, s, "403", "asked for too much", "refuses to install")
	absent(t, out, "was not published")
	absent(t, out, "does not include")
}
