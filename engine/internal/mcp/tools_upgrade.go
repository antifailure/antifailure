package mcp

import (
	"context"
)

// upgrade installs the latest verified community release of the CLI and its
// bundled runner in place, or with the check option reports the latest release
// without touching a file. It is the "always be on the newest Antifailure"
// lane: an agent that keeps a project rehearsing against the current engine
// does not have to leave the session to run an installer by hand.
//
// WHY THE WORK LIVES IN THE COMMAND, NOT HERE. The download, the published
// SHA256 verification and the atomic binary-and-runner swap are the same code
// af update runs, and that code lives in the package that builds the command.
// That package imports this one, so this one can never import it back, and
// reimplementing the swap here would give two instruments that can disagree
// about how an upgrade is applied, which is the exact defect this repository
// keeps finding in its own gates. So the runner is handed in as a function
// value, the same way the machine checks behind check_prerequisites are, and a
// nil value is reported as a refusal rather than as "already up to date": an
// upgrade that could not be attempted is never dressed up as one that found
// nothing to do.
//
// WHAT APPLYING DOES TO THE RUNNING SERVER. Replacing the on-disk binary does
// not disturb this process, which holds its own open file, so a rehearsal in
// flight keeps running the code it started with. The new version does not run
// until the MCP server is restarted, and the projection says so plainly rather
// than letting a caller believe the running tools changed under it.

// UpgradeOutcome is what the injected upgrade runner reports back.
//
// It carries both versions because the two questions a caller has, "am I
// current" and "what did this change", need both, and the runner is the one
// place that knows the installed version and the latest release at the same
// moment. On a check the runner fills Current and Latest and leaves Applied
// false and Path empty. On an apply that changed files it fills all four. On an
// apply that found nothing newer it reports Applied false with the two versions
// equal, which is a clean "already latest" and not a failure.
type UpgradeOutcome struct {
	// Current is the version running before any swap.
	Current string
	// Latest is the latest published stable release the runner saw.
	Latest string
	// Applied is true only when the files on disk were actually replaced.
	Applied bool
	// Path is the installed binary that was, or would be, replaced.
	Path string
}

// upgradeRunner installs the latest release, or with check true only reports
// it. serve.go satisfies it with a closure that calls the command's own update
// path; a test satisfies it with a fake that returns a canned outcome or a
// refusal, so this tool is exercised without a network, a release or a binary
// to swap.
//
// An error is an upgrade that did not happen and left every file unchanged: a
// binary in a location the installer does not own, an enterprise build, a
// platform with no release, a checksum that did not match, or a download that
// did not arrive. The message is the product's own guidance for that case and
// is surfaced to the caller, because unlike a single unreadable diff these
// causes are many and each has a different next step.
type upgradeRunner func(ctx context.Context, check bool) (UpgradeOutcome, error)

// newUpgradeTool builds upgrade.
func newUpgradeTool(p *Project, run upgradeRunner) *Tool {
	return &Tool{
		Name:  "upgrade",
		Title: "Upgrade to the latest release",
		// NOT read only: applying replaces the installed binary and its runner
		// source on disk. The check option changes nothing, but a tool declares
		// one nature and the default action here writes, so the honest
		// declaration is that it writes.
		ReadOnly: false,
		Description: "Install the latest verified community release of the Antifailure CLI and its " +
			"bundled runner in place, so a project keeps rehearsing against the newest engine " +
			"without leaving the session to run an installer. It downloads the release for this " +
			"platform, verifies its published SHA256 checksum, and replaces the binary and the " +
			"runner source atomically; it leaves shell profiles and project files alone. Pass the " +
			"check option to read the latest release and compare it to the installed version " +
			"without changing anything. Applying does not disturb this running server, which keeps " +
			"the code it started with, so the new version takes effect only once the MCP server is " +
			"restarted; the result says so and reports the versions before and after. A binary the " +
			"installer does not own, an enterprise build, a platform with no release, a checksum " +
			"mismatch or a failed download is reported as a refusal that changed nothing, with the " +
			"reason and the next step, never as an upgrade that found nothing to do. When the " +
			"installed version already is the latest, nothing is downloaded and nothing changes.",
		Input: &Schema{
			Type:     "object",
			Required: []string{"project_id"},
			Properties: map[string]*Schema{
				"project_id": projectIDSchema(),
				"check": {
					Type: "boolean",
					Description: "Optional. When true, report the latest release and the installed " +
						"version and change nothing. When absent or false, install the latest " +
						"release if it is newer than the installed one.",
				},
			},
		},
		Handler: func(ctx context.Context, _ *Call, args map[string]any) (any, *Fault) {
			if fault := p.checkAssertion(args); fault != nil {
				return nil, fault
			}
			if run == nil {
				// The command did not wire an upgrade path into this build, so
				// there is nothing to attempt. Reported as a refusal rather
				// than a clean "already latest", because a check that did not
				// run is never a pass, the same rule check_prerequisites holds
				// for a machine probe it could not perform.
				return nil, &Fault{
					Code: FaultUnsupported,
					Detail: "This build cannot upgrade itself in place. Upgrade through the " +
						"package manager that installed it, or reinstall from " +
						"https://antifailure.dev/install.sh.",
				}
			}
			check, _ := args["check"].(bool)
			outcome, err := run(ctx, check)
			if err != nil {
				// Nothing on disk changed, so this is a refusal and not a
				// partial upgrade. The message is the product's own words for
				// this case and carries no path out of this host or any byte
				// from the project, so it is surfaced as the detail: the reason
				// is the guidance, and hiding it would leave the caller with a
				// failure and no next step.
				return nil, &Fault{
					Code:    FaultSafetyUnavailable,
					Detail:  "The upgrade did not run and left every file unchanged. " + safeText(err.Error(), 400),
					wrapped: err,
				}
			}
			return upgradeProjection(check, outcome), nil
		},
	}
}

// upgradeProjection turns the runner's outcome into the tool's document.
//
// The three outcomes a caller must be able to tell apart are kept distinct: a
// check that only looked, an apply that replaced the files, and an apply that
// found the installed version already current and changed nothing. Only the
// second sets restart_required, because only the second put new code on disk
// that this running server is not yet using.
func upgradeProjection(checkedOnly bool, o UpgradeOutcome) any {
	doc := map[string]any{
		"kind":             "upgrade",
		"current_version":  o.Current,
		"latest_version":   o.Latest,
		"applied":          o.Applied,
		"restart_required": o.Applied,
		"installed_path":   o.Path,
	}
	switch {
	case checkedOnly:
		doc["applied"] = false
		doc["restart_required"] = false
		delete(doc, "installed_path")
		if o.Current != "" && o.Current == o.Latest {
			doc["summary"] = "The installed version " + o.Current +
				" is the latest stable release. Nothing to upgrade."
		} else {
			doc["summary"] = "The latest stable release is " + o.Latest +
				", and the installed version is " + o.Current +
				". Run this tool without the check option to install it."
		}
	case o.Applied:
		doc["summary"] = "Installed " + o.Latest + " over " + o.Current +
			", with its checksum verified. This running server keeps the old code until " +
			"the MCP server is restarted; then run 'af runner install' and 'af doctor'."
	default:
		doc["summary"] = "The installed version " + o.Current +
			" is already the latest stable release. Nothing was downloaded and nothing changed."
	}
	return doc
}
