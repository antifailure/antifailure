package cli

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type updateResult struct {
	Version       string `json:"version"`
	InstalledPath string `json:"installed_path"`
	Applied       bool   `json:"applied"`
}

func newUpdateCommand(env *Env) *cobra.Command {
	var check bool
	var installPrefix string
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Install the latest verified CLI release in place",
		Long:  "Downloads the latest stable community release for this platform, verifies the published SHA256 checksum, and replaces this binary and its bundled runner source. It leaves shell profiles and project files alone. Package-managed installations must be upgraded through their package manager; enterprise binaries must use their enterprise distribution. The check option reads the latest release without changing files. For a legacy installer with a separate binary directory, the prefix option names its original installation prefix.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			executable, err := os.Executable()
			if err != nil {
				return err
			}
			executable, err = filepath.EvalSymlinks(executable)
			if err != nil {
				return err
			}
			result, err := performUpdate(cmd.Context(), executable, installPrefix, Version, runtime.GOOS, runtime.GOARCH, latestReleaseURL,
				"https://github.com/antifailure/antifailure/releases/download", releaseHTTPClient(2*time.Minute), check)
			if err != nil {
				return fmt.Errorf("update: %w", err)
			}
			if env.Out.Format == FormatJSON {
				return env.Out.JSON(result)
			}
			if !result.Applied {
				env.Out.Printf("Latest stable release: %s. Installed version: %s. Nothing changed.\n", result.Version, Version)
			} else {
				env.Out.Printf("Installed %s to %s, with its checksum verified.\nShell profiles and project files were left alone.\nRun 'af runner install' to refresh the agent runner, then 'af doctor'.\n", result.Version, result.InstalledPath)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "Show the latest release without changing files")
	cmd.Flags().StringVar(&installPrefix, "prefix", "", "Installer prefix for a legacy custom binary directory")
	return cmd
}

func fetchUpdate(ctx context.Context, client *http.Client, url string, dst io.Writer, limit int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s returned HTTP %d", url, resp.StatusCode)
	}
	n, err := io.Copy(dst, io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return err
	}
	if n > limit {
		return errors.New("release download exceeds the size limit")
	}
	return nil
}

func performUpdate(ctx context.Context, executable, installPrefix, current, goos, goarch, metadataURL, downloadURL string, client *http.Client, check bool) (updateResult, error) {
	r := updateResult{InstalledPath: executable}
	if status, _ := declaredEdition(ctx); status.Name != "community" {
		return r, errors.New("public releases are community binaries; use the enterprise distribution to upgrade this installation")
	}
	if (goos != "darwin" && goos != "linux") || (goarch != "amd64" && goarch != "arm64") {
		return r, fmt.Errorf("no release is available for %s/%s", goos, goarch)
	}
	binDir := filepath.Dir(executable)
	var runner string
	var lock *os.File
	if !check {
		if filepath.Base(executable) != "af" || (filepath.Base(binDir) != "bin" && installPrefix == "") || strings.Contains(filepath.ToSlash(executable), "/Cellar/") || strings.Contains(filepath.ToSlash(executable), "/nix/store/") {
			return r, errors.New("this is not an installer-managed bin/af location; use the package manager or install from https://antifailure.dev/install.sh")
		}
		prefix := filepath.Dir(binDir)
		if installPrefix != "" {
			var err error
			prefix, err = filepath.Abs(installPrefix)
			if err != nil {
				return r, err
			}
		}
		runner = filepath.Join(prefix, "share", "antifailure", "runner")
		var err error
		lock, err = acquireUpdateLock(filepath.Join(binDir, ".af-update-lock")) //nolint:staticcheck // see below
		// SA4023 fires on this under GOOS=windows and only there, because that
		// build's acquireUpdateLock refuses unconditionally, so the comparison
		// really is always true for it. On the two platforms that can
		// self-update it is the check that matters, and Windows never arrives
		// here anyway: the platform is refused further up.
		if err != nil { //nolint:staticcheck // always true on windows only, see above
			return r, err
		}
		defer func() { _ = lock.Close() }()
		if err := recoverUpdate(lock, binDir, runner); err != nil {
			return r, err
		}
		if err := sweepAbandonedStages(binDir); err != nil {
			return r, err
		}
		if st, err := os.Lstat(runner); err != nil || !st.IsDir() {
			return r, errors.New("the bundled runner source is absent or is a link; for a custom install, name its original prefix with the prefix option")
		}
	}
	var metadata strings.Builder
	if err := fetchUpdate(ctx, client, metadataURL, &metadata, 1<<20); err != nil {
		return r, err
	}
	var release struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.Unmarshal([]byte(metadata.String()), &release); err != nil {
		return r, err
	}
	if _, valid := stableVersion(release.Tag); !valid || release.Draft || release.Prerelease {
		return r, errors.New("release metadata did not name a published stable version")
	}
	r.Version = release.Tag
	if check {
		return r, nil
	}
	if installed, valid := stableVersion(current); valid {
		latest, _ := stableVersion(release.Tag)
		order := 0
		for i := range installed {
			if installed[i] < latest[i] {
				order = -1
				break
			}
			if installed[i] > latest[i] {
				order = 1
				break
			}
		}
		if order > 0 {
			return r, errors.New("the installed version is newer than the latest stable release; refusing to downgrade")
		}
		if order == 0 {
			return r, nil
		}
	}
	stage, err := os.MkdirTemp(binDir, ".af-update-")
	if err != nil {
		return r, err
	}
	keepStage := false
	defer func() {
		if !keepStage {
			_ = os.RemoveAll(stage)
		}
	}()
	name := "antifailure_" + strings.TrimPrefix(release.Tag, "v") + "_" + goos + "_" + goarch
	base := downloadURL + "/" + release.Tag
	var sums strings.Builder
	if err := fetchUpdate(ctx, client, base+"/checksums.txt", &sums, 1<<20); err != nil {
		return r, fmt.Errorf("read published checksums: %w", err)
	}
	expected := ""
	for _, line := range strings.Split(sums.String(), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == name+".tar.gz" {
			if expected != "" {
				return r, errors.New("duplicate checksum entry for this archive")
			}
			expected = fields[0]
		}
	}
	decoded, err := hex.DecodeString(expected)
	if err != nil || len(decoded) != sha256.Size {
		return r, errors.New("no valid SHA256 checksum names this archive")
	}
	archive, err := os.Create(filepath.Join(stage, "release.tar.gz"))
	if err != nil {
		return r, err
	}
	hash := sha256.New()
	err = fetchUpdate(ctx, client, base+"/"+name+".tar.gz", io.MultiWriter(archive, hash), 128<<20)
	closeErr := archive.Close()
	if err != nil {
		return r, err
	}
	if closeErr != nil {
		return r, closeErr
	}
	if hex.EncodeToString(hash.Sum(nil)) != strings.ToLower(expected) {
		return r, errors.New("archive checksum mismatch; the installed version was not changed")
	}
	if err := unpackUpdate(archive.Name(), stage, name); err != nil {
		return r, err
	}
	installed, err := os.Stat(executable)
	if err != nil {
		return r, err
	}
	if err := os.Chmod(filepath.Join(stage, "af"), installed.Mode().Perm()); err != nil {
		return r, err
	}
	journal, err := json.Marshal(struct {
		Stage  string `json:"stage"`
		Runner string `json:"runner"`
	}{stage, runner})
	if err != nil {
		return r, err
	}
	if err := writeUpdateJournal(lock, string(journal)); err != nil {
		return r, err
	}
	result, err := commitUpdate(r, stage, runner, os.Rename)
	if err != nil {
		if _, backupErr := os.Stat(filepath.Join(stage, "old-runner")); backupErr == nil {
			keepStage = true
			err = fmt.Errorf("%w; the original runner is preserved at %s", err, filepath.Join(stage, "old-runner"))
		}
	}
	if !keepStage {
		if journalErr := writeUpdateJournal(lock, ""); journalErr != nil {
			return result, errors.Join(err, journalErr)
		}
	}
	return result, err
}

// sweepAbandonedStages removes the staging directories a killed update left.
//
// A stage holds the downloaded archive and the release unpacked beside it,
// which is tens of megabytes, and the deferred cleanup that removes it does not
// run when the process is killed. Nothing looked at them afterwards, so a
// machine whose update was interrupted twice carried both of them in its bin
// directory for good, and the second one is invisible: it is a dot directory
// next to the binary somebody only ever runs.
//
// Only a stage that never reached the commit is removed, and old-runner is the
// marker for that, because commitUpdate creates it as its first act and a stage
// holding one may be the only copy of the installed runner source. The
// installation lock is held by the time this runs, so no stage here belongs to
// an update still in progress.
func sweepAbandonedStages(binDir string) error {
	entries, err := os.ReadDir(binDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		// IsDir is false for a link, which is deliberately skipped rather than
		// removed: the name is inside an installation directory, and a link
		// there was put there by something that is not this command.
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), ".af-update-") {
			continue
		}
		stage := filepath.Join(binDir, entry.Name())
		_, err := os.Stat(filepath.Join(stage, "old-runner"))
		if err == nil {
			continue
		}
		if !os.IsNotExist(err) {
			return err
		}
		if err := os.RemoveAll(stage); err != nil {
			return err
		}
	}
	return nil
}

func writeUpdateJournal(lock *os.File, body string) error {
	name := lock.Name() + ".json"
	if body == "" {
		err := os.Remove(name)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(name), ".af-update-journal-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.WriteString(body); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), name)
}

func recoverUpdate(lock *os.File, binDir, runner string) error {
	f, err := os.Open(lock.Name() + ".json")
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return err
	}
	if len(b) == 0 {
		return nil
	}
	var journal struct {
		Stage  string `json:"stage"`
		Runner string `json:"runner"`
	}
	if err := json.Unmarshal(b, &journal); err != nil {
		return fmt.Errorf("the update recovery journal is unreadable; no files were changed: %w", err)
	}
	stage := journal.Stage
	if journal.Runner != runner {
		return errors.New("an interrupted update used a different install prefix; rerun with that original prefix to recover it")
	}
	if len(b) > 4096 || filepath.Dir(stage) != binDir || !strings.HasPrefix(filepath.Base(stage), ".af-update-") {
		return errors.New("the update recovery journal has an invalid staging path; no files were changed")
	}
	st, err := os.Lstat(stage)
	if os.IsNotExist(err) {
		return writeUpdateJournal(lock, "")
	}
	if err != nil || !st.IsDir() {
		return errors.New("the update recovery directory is not a regular directory")
	}
	backup := filepath.Join(stage, "old-runner")
	if _, err := os.Stat(backup); err == nil {
		// A staged binary still present means the atomic commit did not happen.
		// Restore the matching source before trying another update.
		if _, err := os.Stat(filepath.Join(stage, "af")); err == nil {
			if _, err := os.Lstat(runner); err == nil {
				if err := os.Rename(runner, filepath.Join(stage, "interrupted-runner")); err != nil {
					return err
				}
			} else if !os.IsNotExist(err) {
				return err
			}
			if err := os.Rename(backup, runner); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.RemoveAll(stage); err != nil {
		return err
	}
	return writeUpdateJournal(lock, "")
}

func unpackUpdate(archive, stage, root string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	decoded := &io.LimitedReader{R: gz, N: (256 << 20) + 1}
	tr := tar.NewReader(decoded)
	seen := map[string]bool{}
	var total int64
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		clean := path.Clean(h.Name)
		if clean != strings.TrimSuffix(h.Name, "/") || (clean != root && !strings.HasPrefix(clean, root+"/")) || strings.Contains(clean, "\\") {
			return errors.New("unsafe path in release archive")
		}
		if h.Typeflag != tar.TypeDir && h.Typeflag != tar.TypeReg {
			return errors.New("release archive contains a link or unsupported entry")
		}
		rel := strings.TrimPrefix(clean, root+"/")
		if rel != "af" && rel != "runner" && !strings.HasPrefix(rel, "runner/") {
			continue
		}
		if seen[rel] {
			return errors.New("duplicate file in release archive")
		}
		seen[rel] = true
		target := filepath.Join(stage, filepath.FromSlash(rel))
		if h.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
			continue
		}
		if h.Size < 0 || h.Size > (256<<20)-total {
			return errors.New("unpacked release exceeds the size limit")
		}
		total += h.Size
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		mode := os.FileMode(0644)
		if rel == "af" {
			mode = 0755
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return err
		}
		_, err = io.CopyN(out, tr, h.Size)
		closeErr := out.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	// Read the gzip footer too. A tar end marker alone does not verify the
	// compressed stream, and ignored files must not evade the extraction cap.
	if _, err := io.Copy(io.Discard, decoded); err != nil {
		return err
	}
	if decoded.N <= 0 {
		return errors.New("unpacked release exceeds the size limit")
	}
	for _, required := range []string{"af", "runner/src/main.ts", "runner/package.json", "runner/package-lock.json"} {
		st, err := os.Stat(filepath.Join(stage, required))
		if err != nil || !st.Mode().IsRegular() || st.Size() == 0 {
			return fmt.Errorf("release is missing required file %s", required)
		}
	}
	return nil
}

func commitUpdate(result updateResult, stage, runner string, rename func(string, string) error) (updateResult, error) {
	backup := filepath.Join(stage, "old-runner")
	if err := rename(runner, backup); err != nil {
		return result, err
	}
	if err := rename(filepath.Join(stage, "runner"), runner); err != nil {
		return result, errors.Join(err, rename(backup, runner))
	}
	if err := rename(filepath.Join(stage, "af"), result.InstalledPath); err != nil {
		// Move the new source out before restoring the old one. The binary has
		// not been replaced, so a recoverable failure keeps the original pair.
		moveErr := rename(runner, filepath.Join(stage, "runner"))
		if moveErr != nil {
			return result, errors.Join(err, moveErr)
		}
		return result, errors.Join(err, rename(backup, runner))
	}
	result.Applied = true
	return result, nil
}
