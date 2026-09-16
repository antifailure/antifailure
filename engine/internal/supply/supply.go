// Package supply is the security check family for a dependency change that
// loosens the supply chain in a way an advisory scan does not catch: a new
// install hook, an install step that downloads and runs a binary, or a
// dependency whose source moved off the package registry to a git URL or a
// tarball.
//
// It is deliberately NOT an advisory scanner. A known vulnerable version is
// already caught by the repository's npm-advisories and known-vulnerabilities
// CI gates, and reimplementing that here would be a second, weaker copy. This
// family reads the behavioural and structural change instead: what a dependency
// change ADDS. That is why every rule reasons about the DIFF, the lines the
// change adds to a manifest or a lockfile, and never the absolute dependency
// tree of the branch. A lockfile already carrying a git source is not this
// change's doing; a line this change ADDS that points at one is.
//
// It is a deny-it family: its findings refuse a change on policy grounds rather
// than proving a runtime exploit, so the spine gives them the policy-denial
// exit code. The default level is warn, because a dependency change carries
// legitimate noise and a wall of red on every bump is a muted check; a project
// raises it to fail once its dependency changes are quiet.
//
// It imports nothing under ee. Supply-chain analysis is in every edition.
package supply

import (
	"context"
	"path"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/change"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/security"
)

// Name is the family id and the namespace of every key and finding rule it
// owns: the keys are "security.supply_chain.<rule>".
const Name = "supply_chain"

const (
	// KeyInstallScript is a lifecycle install hook added to a package manifest.
	// An install hook runs arbitrary code on every install, in CI and on every
	// developer's machine, which is the foothold a supply-chain attack uses.
	KeyInstallScript report.PolicyKey = "security.supply_chain.install_script_added"
	// KeyBinaryDownload is an install step that downloads a binary or a script
	// and runs it, the shape of a compromised package fetching its payload.
	KeyBinaryDownload report.PolicyKey = "security.supply_chain.binary_download_in_install"
	// KeyRegistrySource is a dependency whose source moved off the registry to
	// a git URL, a tarball or a local path, which is neither versioned nor
	// signed the way a registry release is.
	KeyRegistrySource report.PolicyKey = "security.supply_chain.registry_source_changed"
)

type family struct{}

// New returns the supply-chain family.
func New() security.Family { return family{} }

// Name identifies the family.
func (family) Name() string { return Name }

// Surfaces is the dependency surface: a lockfile or a package manifest change.
// The router runs the family when the diff touches one.
func (family) Surfaces() []change.Surface { return []change.Surface{change.SurfaceDependency} }

// Checks is the supply-chain check the family contributes to the plan and report.
func (family) Checks() []change.Check { return []change.Check{change.CheckSupplyChain} }

// Licensed is empty: supply-chain analysis is in every edition.
func (family) Licensed() string { return "" }

// Keys declares the family's policy keys. Every key defaults to warn: a
// dependency change is noisy, and a check that reddens every bump is one a team
// mutes, so it reports by default and a project raises it to fail once its
// dependency changes are clean. The exit is read from the spine's ExitFor, so
// the family cannot declare an exit the gate would not honour; the whole family
// is deny-it, exit 6.
func (family) Keys() []security.KeySpec {
	return []security.KeySpec{
		{Key: KeyInstallScript, Default: report.LevelWarn,
			Title: "A dependency change added a lifecycle install hook.", Docs: "concepts/security",
			Exit: security.ExitFor(KeyInstallScript)},
		{Key: KeyBinaryDownload, Default: report.LevelWarn,
			Title: "A dependency change added an install step that downloads and runs a binary.", Docs: "concepts/security",
			Exit: security.ExitFor(KeyBinaryDownload)},
		{Key: KeyRegistrySource, Default: report.LevelWarn,
			Title: "A dependency change moved a dependency source off the registry.", Docs: "concepts/security",
			Exit: security.ExitFor(KeyRegistrySource)},
	}
}

// installHooks are the package.json lifecycle scripts that run code on install.
var installHooks = []string{"preinstall", "install", "postinstall", "prepare", "prepublish"}

// offRegistrySchemes are the dependency source forms that are not a registry
// release: a git source or a forge shorthand. Deliberately only the
// unambiguous version-control schemes, not file: or link:, which a local
// monorepo uses legitimately and which read the same in prose.
var offRegistrySchemes = []string{"git+", "github:", "gitlab:", "bitbucket:"}

// metadataKeys are package.json fields whose value is a URL by design and is
// NOT a dependency source. A repository field pointing at a git URL is
// metadata, not a dependency resolved to a fork, and flagging it is the false
// positive that gets the rule switched off.
var metadataKeys = map[string]bool{
	"repository": true, "homepage": true, "bugs": true, "funding": true,
	"url": true, "author": true, "license": true, "source": true,
}

// downloadTools are the commands an install step uses to fetch a payload.
var downloadTools = []string{"curl ", "curl|", "wget ", "wget|", "invoke-webrequest", "iwr "}

// runsAShell are the ways a fetched payload is executed inline.
var runsAShell = []string{"| sh", "|sh", "| bash", "|bash", "chmod +x", "chmod a+x", "| python", "|python"}

// Probe reads the dependency diff and reports what a change added that loosens
// the supply chain. It reads no environment and drives nothing: the analysis is
// of the lines the change adds. The level of every finding comes from the
// manifest through in.Policy, never from this family.
func (f family) Probe(_ context.Context, in security.Input) ([]report.Finding, error) {
	files, ok := in.DependencyDiff()
	if !ok {
		// The router attached no dependency diff, so there is nothing to read.
		// A reader that could not look reports no finding rather than a pass it
		// did not earn; there is nothing to fail on here either.
		return nil, nil
	}

	install := in.Policy.Level(KeyInstallScript)
	binary := in.Policy.Level(KeyBinaryDownload)
	registry := in.Policy.Level(KeyRegistrySource)

	seen := map[string]bool{}
	var out []report.Finding
	emit := func(key report.PolicyKey, level report.Level, file, detail, fix string) {
		if level == report.LevelIgnore {
			return
		}
		dedupe := string(key) + "\x00" + file
		if seen[dedupe] {
			return
		}
		seen[dedupe] = true
		out = append(out, report.Finding{
			Rule: string(key), Level: level, Where: file,
			Title: titleFor(key), Detail: detail, Fix: fix,
		})
	}

	for _, file := range files {
		isManifest := isPackageManifest(file.Path)
		for _, line := range file.Added {
			low := strings.ToLower(line)

			if isManifest && addsInstallHook(low) {
				emit(KeyInstallScript, install, file.Path,
					"The change adds a lifecycle install hook to "+file.Path+". An install "+
						"hook runs on every install, in CI and on every machine that installs "+
						"the project, so it is the step a supply-chain attack reaches for.",
					"Confirm the hook is one the project wrote and needs. If a dependency "+
						"brought it in, that dependency now runs code on install; pin it and "+
						"read what the hook does. The line itself is not printed here.")
			}

			if addsBinaryDownload(low) {
				emit(KeyBinaryDownload, binary, file.Path,
					"The change adds a step in "+file.Path+" that downloads something and runs "+
						"it. A fetch piped into a shell is how a compromised package pulls its "+
						"payload past a review that read only the version numbers.",
					"Do not download and execute in an install step. Vendor the artifact, or "+
						"install it through the package manager with a pinned, checksummed "+
						"version. The line itself is not printed here.")
			}

			if addsOffRegistrySource(low) {
				emit(KeyRegistrySource, registry, file.Path,
					"The change points a dependency in "+file.Path+" at a source off the "+
						"package registry, a git URL, a tarball or a local path, which is "+
						"neither versioned nor signed the way a registry release is and can "+
						"change under the same reference.",
					"Depend on a registry release with a pinned version, or if a fork is "+
						"genuinely needed, pin it to an immutable commit and record why. The "+
						"reference itself is not printed here.")
			}
		}
	}
	return out, nil
}

// isPackageManifest reports whether a path is a manifest that can declare an
// install hook, as opposed to a lockfile, which cannot.
func isPackageManifest(p string) bool {
	base := strings.ToLower(path.Base(p))
	return base == "package.json"
}

// addsInstallHook reports whether a lowered added line declares an install
// lifecycle script, matched as a quoted JSON key followed by a colon so a
// dependency merely NAMED "postinstall" in a description does not trip it.
func addsInstallHook(low string) bool {
	for _, h := range installHooks {
		key := `"` + h + `"`
		if i := strings.Index(low, key); i >= 0 {
			rest := strings.TrimSpace(low[i+len(key):])
			if strings.HasPrefix(rest, ":") {
				return true
			}
		}
	}
	return false
}

// addsBinaryDownload reports whether a lowered added line downloads a payload
// and runs it: a fetch tool together with an inline shell or an executable bit.
// Both halves are required, so a plain documentation URL or a lone curl in a
// test command does not trip it.
func addsBinaryDownload(low string) bool {
	fetches := false
	for _, t := range downloadTools {
		if strings.Contains(low, t) {
			fetches = true
			break
		}
	}
	if !fetches {
		return false
	}
	for _, r := range runsAShell {
		if strings.Contains(low, r) {
			return true
		}
	}
	return false
}

// addsOffRegistrySource reports whether a lowered added line points a
// dependency at a non-registry source. It reads the line as a JSON key and
// value: the value must START with a version-control scheme, so a scheme
// mentioned in prose does not trip it, and the key must not be a metadata field
// whose value is a URL by design, so a repository or homepage does not either.
func addsOffRegistrySource(low string) bool {
	i := strings.Index(low, `":`)
	if i < 0 {
		return false
	}
	if metadataKeys[lastQuoted(low[:i+1])] {
		return false
	}
	value, ok := firstQuoted(low[i+len(`":`):])
	if !ok {
		return false
	}
	for _, s := range offRegistrySchemes {
		if strings.HasPrefix(value, s) {
			return true
		}
	}
	return false
}

// lastQuoted returns the contents of the last "..." in s, which for a JSON key
// and colon is the key. Empty when there is no quoted run.
func lastQuoted(s string) string {
	end := strings.LastIndexByte(s, '"')
	if end < 0 {
		return ""
	}
	start := strings.LastIndexByte(s[:end], '"')
	if start < 0 {
		return ""
	}
	return s[start+1 : end]
}

// firstQuoted returns the contents of the first "..." in s, which after a
// colon is the value, and whether one was found.
func firstQuoted(s string) (string, bool) {
	start := strings.IndexByte(s, '"')
	if start < 0 {
		return "", false
	}
	rest := s[start+1:]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return rest, true // an unterminated value, still worth reading its head
	}
	return rest[:end], true
}

func titleFor(key report.PolicyKey) string {
	switch key {
	case KeyInstallScript:
		return "A dependency change added a lifecycle install hook."
	case KeyBinaryDownload:
		return "A dependency change added an install step that downloads and runs a binary."
	default:
		return "A dependency change moved a dependency source off the registry."
	}
}
