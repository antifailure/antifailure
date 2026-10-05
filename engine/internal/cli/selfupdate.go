package cli

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/rand"
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
	if (goos != "darwin" && goos != "linux" && goos != "windows") || (goarch != "amd64" && goarch != "arm64") {
		return r, fmt.Errorf("no release is available for %s/%s", goos, goarch)
	}
	binDir := filepath.Dir(executable)
	var runner string
	var lock *os.File
	if !check {
		if !installerManaged(goos, executable, installPrefix) {
			return r, fmt.Errorf("this is not an installer-managed bin/%s location; use the package manager or install from %s", releaseBinary(goos), installerURL(goos))
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
		lock, err = acquireUpdateLock(filepath.Join(binDir, ".af-update-lock"))
		if err != nil {
			return r, err
		}
		defer func() { _ = lock.Close() }()
		if err := recoverUpdate(lock, binDir, runner); err != nil {
			return r, err
		}
		if err := sweepAbandonedStages(binDir); err != nil {
			return r, err
		}
		sweepReplacedExecutable(goos, executable)
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
	archiveName := name + releaseArchiveExt(goos)
	base := downloadURL + "/" + release.Tag
	var sums strings.Builder
	if err := fetchUpdate(ctx, client, base+"/checksums.txt", &sums, 1<<20); err != nil {
		return r, fmt.Errorf("read published checksums: %w", err)
	}
	expected := ""
	for _, line := range strings.Split(sums.String(), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == archiveName {
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
	archive, err := os.Create(filepath.Join(stage, "release"+releaseArchiveExt(goos)))
	if err != nil {
		return r, err
	}
	hash := sha256.New()
	err = fetchUpdate(ctx, client, base+"/"+archiveName, io.MultiWriter(archive, hash), 128<<20)
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
	if err := unpackUpdate(archive.Name(), stage, name, goos); err != nil {
		return r, err
	}
	installed, err := os.Stat(executable)
	if err != nil {
		return r, err
	}
	// Windows has no mode bits to carry over: Chmod there only toggles the
	// read only attribute, and copying 0666 back onto the file says nothing.
	if goos != "windows" {
		if err := os.Chmod(filepath.Join(stage, "af"), installed.Mode().Perm()); err != nil {
			return r, err
		}
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
	result, err := commitUpdate(r, stage, runner, goos, os.Rename)
	if err != nil && goos == "windows" {
		// The likeliest cause by far, and one nothing in a sharing violation
		// names: an af process that is still running, an MCP server an editor
		// keeps open being the usual one, holds the runner source open.
		err = fmt.Errorf("%w; on Windows a running af process, such as an MCP server an editor started, can hold these files open, so close it and run the update again", err)
	}
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
	// Read and closed here rather than closed on return. Recovery ends by
	// removing this file, and Windows refuses to remove a file that is still
	// open, because Go opens it without FILE_SHARE_DELETE. A deferred close
	// would have made every recovery on Windows fail at its last step and
	// leave the journal to be replayed forever.
	b, err := io.ReadAll(io.LimitReader(f, 4097))
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
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

// releaseBinary is the file name of the CLI inside a release and inside an
// installation for goos.
func releaseBinary(goos string) string {
	if goos == "windows" {
		return "af.exe"
	}
	return "af"
}

// releaseArchiveExt is how a release for goos is packaged. Windows gets a zip
// because that is what Windows can open without installing anything, and it is
// what install.ps1 expands.
func releaseArchiveExt(goos string) string {
	if goos == "windows" {
		return ".zip"
	}
	return ".tar.gz"
}

func installerURL(goos string) string {
	if goos == "windows" {
		return "https://antifailure.dev/install.ps1"
	}
	return "https://antifailure.dev/install.sh"
}

// installerManaged reports whether executable sits where an installer put it,
// which is the only layout this command knows how to replace together with its
// runner source. Windows file names are case insensitive, so AF.EXE under BIN is
// the same installation as af.exe under bin.
func installerManaged(goos, executable, installPrefix string) bool {
	binDir := filepath.Dir(executable)
	same := func(a, b string) bool { return a == b }
	if goos == "windows" {
		same = strings.EqualFold
	}
	if !same(filepath.Base(executable), releaseBinary(goos)) {
		return false
	}
	if !same(filepath.Base(binDir), "bin") && installPrefix == "" {
		return false
	}
	slashed := filepath.ToSlash(executable)
	return !strings.Contains(slashed, "/Cellar/") && !strings.Contains(slashed, "/nix/store/")
}

// replacedPrefix is the start of the name a replaced Windows binary is moved
// to. Windows will not overwrite or delete the image of a running process, but
// it will rename one, so the update moves the running af.exe aside under this
// name, puts the new one where it was, and the next af to start removes what
// was moved aside once nothing is running it.
func replacedPrefix(executable string) string {
	return filepath.Base(executable) + ".old-"
}

// sweepReplacedExecutable removes binaries an earlier Windows update moved
// aside. It is best effort by design: a replaced binary still running, an MCP
// server an editor keeps open being the usual case, cannot be deleted and is
// left for a later start, and nothing here can fail the command that started.
func sweepReplacedExecutable(goos, executable string) {
	if goos != "windows" || !strings.EqualFold(filepath.Base(executable), releaseBinary(goos)) {
		return
	}
	dir := filepath.Dir(executable)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	prefix := strings.ToLower(replacedPrefix(executable))
	for _, entry := range entries {
		// Only the exact shape an update writes, the prefix and twelve hex
		// digits, so a file somebody named af.exe.old-backup by hand is theirs
		// and is never touched.
		name := strings.ToLower(entry.Name())
		if !entry.Type().IsRegular() || !strings.HasPrefix(name, prefix) || !isReplacedSuffix(name[len(prefix):]) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, entry.Name()))
	}
}

// replacedSuffixLen is the length of the random suffix commitUpdate gives a
// binary it moves aside: six random bytes written as hex.
const replacedSuffixLen = 12

func isReplacedSuffix(suffix string) bool {
	if len(suffix) != replacedSuffixLen {
		return false
	}
	_, err := hex.DecodeString(suffix)
	return err == nil
}

// hostGOOS and hostExecutable are what a starting process sweeps with. They
// are variables so a test can run the real entry point as a Windows
// installation without being one.
var (
	hostGOOS       = runtime.GOOS
	hostExecutable = func() (string, error) {
		exe, err := os.Executable()
		if err != nil {
			return "", err
		}
		return filepath.EvalSymlinks(exe)
	}
)

// sweepAtStart is called once as the process starts. A Windows update cannot
// delete the binary it replaced, because that binary is the one running the
// update, so the cleanup belongs to the next af that starts. Without it every
// update left a full copy of the previous release beside the new one.
func sweepAtStart() {
	if hostGOOS != "windows" {
		return
	}
	exe, err := hostExecutable()
	if err != nil {
		return
	}
	sweepReplacedExecutable(hostGOOS, exe)
}

// releaseUnpacker applies the same rules to every entry whatever the archive
// format, so a zip cannot carry what a tarball is refused for.
type releaseUnpacker struct {
	stage, root, binary string
	seen                map[string]bool
	total               int64
}

func (u *releaseUnpacker) entry(name string, dir, regular bool, size int64, body io.Reader) error {
	clean := path.Clean(name)
	if clean != strings.TrimSuffix(name, "/") || (clean != u.root && !strings.HasPrefix(clean, u.root+"/")) || strings.Contains(clean, "\\") {
		return errors.New("unsafe path in release archive")
	}
	if !dir && !regular {
		return errors.New("release archive contains a link or unsupported entry")
	}
	rel := strings.TrimPrefix(clean, u.root+"/")
	// Checked before anything is ignored. An archive carrying two entries of
	// one name was not built by the release pipeline, whichever name it is.
	if u.seen[rel] {
		return errors.New("duplicate file in release archive")
	}
	u.seen[rel] = true
	if rel != u.binary && rel != "runner" && !strings.HasPrefix(rel, "runner/") {
		return nil
	}
	// The binary is staged as af on every platform, so the recovery journal,
	// which reads a staged af as "the commit did not happen", means the same
	// thing whichever platform wrote it.
	if rel == u.binary {
		rel = "af"
	}
	target := filepath.Join(u.stage, filepath.FromSlash(rel))
	if dir {
		return os.MkdirAll(target, 0755)
	}
	if size < 0 || size > (256<<20)-u.total {
		return errors.New("unpacked release exceeds the size limit")
	}
	u.total += size
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
	n, err := io.Copy(out, io.LimitReader(body, size+1))
	closeErr := out.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if n != size {
		return errors.New("a release archive entry is not the size it declares")
	}
	return nil
}

func unpackUpdate(archive, stage, root, goos string) error {
	u := &releaseUnpacker{stage: stage, root: root, binary: releaseBinary(goos), seen: map[string]bool{}}
	var err error
	if goos == "windows" {
		err = u.unpackZip(archive)
	} else {
		err = u.unpackTarGz(archive)
	}
	if err != nil {
		return err
	}
	for _, required := range []string{"af", "runner/src/main.ts", "runner/package.json", "runner/package-lock.json"} {
		name := required
		if required == "af" {
			name = u.binary
		}
		st, err := os.Stat(filepath.Join(stage, required))
		if err != nil || !st.Mode().IsRegular() || st.Size() == 0 {
			return fmt.Errorf("release is missing required file %s", name)
		}
	}
	return nil
}

func (u *releaseUnpacker) unpackTarGz(archive string) error {
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
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if err := u.entry(h.Name, h.Typeflag == tar.TypeDir, h.Typeflag == tar.TypeReg, h.Size, tr); err != nil {
			return err
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
	return nil
}

// unpackZip reads every entry to its end, ignored ones included, because the
// zip reader checks an entry's CRC only when it reaches the end of it. An entry
// skipped unread would be an entry nothing verified.
func (u *releaseUnpacker) unpackZip(archive string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer func() { _ = zr.Close() }()
	var declared uint64
	for _, f := range zr.File {
		declared += f.UncompressedSize64
		if declared > 256<<20 {
			return errors.New("unpacked release exceeds the size limit")
		}
		mode := f.Mode()
		dir := mode.IsDir()
		regular := mode.IsRegular()
		rc, err := f.Open()
		if err != nil {
			return err
		}
		err = u.entry(f.Name, dir, regular, int64(f.UncompressedSize64), rc)
		if err == nil {
			// Drain what entry did not read, which is the whole of an ignored
			// entry, so its checksum is verified as well.
			_, err = io.Copy(io.Discard, io.LimitReader(rc, (256<<20)+1))
		}
		closeErr := rc.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func commitUpdate(result updateResult, stage, runner, goos string, rename func(string, string) error) (updateResult, error) {
	backup := filepath.Join(stage, "old-runner")
	if err := rename(runner, backup); err != nil {
		return result, err
	}
	if err := rename(filepath.Join(stage, "runner"), runner); err != nil {
		return result, errors.Join(err, rename(backup, runner))
	}
	// Move the new source out before restoring the old one. The binary has not
	// been replaced, so a recoverable failure keeps the original pair.
	restoreRunner := func(err error) (updateResult, error) {
		if moveErr := rename(runner, filepath.Join(stage, "runner")); moveErr != nil {
			return result, errors.Join(err, moveErr)
		}
		return result, errors.Join(err, rename(backup, runner))
	}
	if goos == "windows" {
		// Windows refuses to replace the image of a running process, and the
		// running process is this one, so a rename over af.exe can never
		// succeed. It does allow the running image to be renamed, so the
		// binary is moved aside first and the new one takes its name. The two
		// renames are adjacent; a process killed exactly between them leaves
		// no af.exe, only the moved aside copy, and rerunning install.ps1
		// restores it.
		var suffix [replacedSuffixLen / 2]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			return restoreRunner(err)
		}
		aside := filepath.Join(filepath.Dir(result.InstalledPath), replacedPrefix(result.InstalledPath)+hex.EncodeToString(suffix[:]))
		if err := rename(result.InstalledPath, aside); err != nil {
			return restoreRunner(err)
		}
		if err := rename(filepath.Join(stage, "af"), result.InstalledPath); err != nil {
			if backErr := rename(aside, result.InstalledPath); backErr != nil {
				return result, errors.Join(err, backErr)
			}
			return restoreRunner(err)
		}
		result.Applied = true
		return result, nil
	}
	if err := rename(filepath.Join(stage, "af"), result.InstalledPath); err != nil {
		return restoreRunner(err)
	}
	result.Applied = true
	return result, nil
}
