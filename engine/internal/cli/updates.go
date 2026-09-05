package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/internal/manifest"
)

const latestReleaseURL = "https://api.github.com/repos/antifailure/antifailure/releases/latest"

// releaseHTTPClient is the only client that talks to a release source, and the
// redirect policy is the reason it exists in one place.
//
// Redirects have to be followed, because GitHub answers a download by sending
// the caller to its object store, and a redirect is the one part of the
// exchange the far end chooses. Left at the default policy, a redirect to plain
// HTTP is followed silently: for the version lookup that lets anybody on the
// path decide which release this machine believes is current, and for the
// download it lets them supply the bytes the published checksum is compared
// against. Replacing the policy also drops net/http's own ten hop limit, so
// this states it rather than losing it.
func releaseHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" {
			return errors.New("a release source redirected away from HTTPS")
		}
		if len(via) >= 10 {
			return errors.New("too many release redirects")
		}
		return nil
	}}
}

func checkCLIRelease(ctx context.Context, _ *Env, _ Prober) CheckResult {
	if status, _ := declaredEdition(ctx); status.Name != "community" {
		return CheckResult{Name: "CLI version", Status: CheckSkip, Detail: "enterprise distribution: public community release versions do not describe this binary", Remediation: "Use the enterprise distribution's upgrade procedure."}
	}
	return releaseCheck(ctx, Version, latestReleaseURL, releaseHTTPClient(3*time.Second))
}

func stableVersion(v string) ([3]uint64, bool) {
	var result [3]uint64
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) != 3 {
		return result, false
	}
	for i, p := range parts {
		if p == "" || (len(p) > 1 && p[0] == '0') {
			return result, false
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return result, false
			}
		}
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return result, false
		}
		result[i] = n
	}
	return result, true
}

func releaseCheck(ctx context.Context, current, endpoint string, client *http.Client) CheckResult {
	r := CheckResult{Name: "CLI version", Status: CheckWarn,
		Remediation: "Run 'af update' to install the latest release. It verifies the published checksum before replacing this binary."}
	installed, ok := stableVersion(current)
	if !ok {
		r.Detail = current + ": not a stable release version, so freshness cannot be checked"
		return r
	}
	r.Detail = current + ": could not check the latest release"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return r
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		r.Detail += ": " + err.Error()
		return r
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		r.Detail += fmt.Sprintf(": HTTP %d", resp.StatusCode)
		return r
	}
	var release struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&release); err != nil {
		r.Detail += ": invalid release response"
		return r
	}
	latest, valid := stableVersion(release.Tag)
	if !valid || release.Draft || release.Prerelease {
		r.Detail += ": response did not name a published stable release"
		return r
	}
	for i := range latest {
		if installed[i] < latest[i] {
			r.Status = CheckFail
			r.Detail = current + " is older than the latest release, " + release.Tag
			return r
		}
		if installed[i] > latest[i] {
			r.Detail = current + " is newer than the published release, " + release.Tag
			r.Remediation = "This version is not the latest published stable release. Check its provenance with 'af version'; 'af update' refuses to downgrade it."
			return r
		}
	}
	r.Status = CheckPass
	r.Detail = current + " matches the latest release"
	// The remediation the failing paths carry says to run 'af update', and
	// leaving it on the passing one told somebody already on the newest release
	// to install it. Text mode prints remediations for problems only, so it was
	// invisible there and shipped in the JSON every script reads.
	r.Remediation = "No action needed. This is the latest published stable release."
	return r
}

func checkProjectManifest(_ context.Context, env *Env, _ Prober) CheckResult {
	r := CheckResult{Name: "Project manifest", Status: CheckWarn,
		Remediation: "Run 'af init' to create the manifest, then 'af doctor' again. No project file was changed."}
	path, err := manifest.Find(env.WorkDir)
	if err != nil {
		r.Detail = "no readable antifailure.yaml was found; this project is not ready for 'af up'"
		return r
	}
	if _, err := manifest.Load(path); err != nil {
		r.Status = CheckFail
		r.Detail = err.Error()
		r.Remediation = "Fix the reported manifest problems, then run 'af doctor' again. No project file was changed."
		return r
	}
	r.Status = CheckPass
	r.Detail = path + " is valid"
	r.Remediation = "No action needed. 'af start' shows the remaining steps to a first result."
	return r
}
