package cli

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

// The runner is a separate program in a separate language, and installing it
// is the one rough edge between a checkout and a working check.
//
// It is a copy rather than a download, because the source that ships with a
// release is the source that release was tested with. Fetching it separately
// would let the two drift, and a runner one version ahead of the engine that
// speaks to it is a failure nobody can read.

// RunnerHome is where the runner is installed.
func RunnerHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".antifailure", "runner"), nil
}

// RunnerInstallJSON is the machine readable result.
type RunnerInstallJSON struct {
	Path     string `json:"path"`
	Files    int    `json:"files"`
	Node     string `json:"node"`
	Browser  string `json:"browser"`
	Complete bool   `json:"complete"`
}

func newRunnerCommand(e *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runner",
		Short: "Install and check the agent runner",
		Long: strings.TrimSpace(`
The runner drives a real browser, so it is a separate program in a separate
language. It is installed from a copy that ships with this engine rather than
downloaded, because the source a release was tested with is the source that
release should run.`),
	}
	cmd.AddCommand(newRunnerInstallCommand(e))
	cmd.AddCommand(newRunnerCheckCommand(e))
	return cmd
}

func newRunnerInstallCommand(e *Env) *cobra.Command {
	var from string
	var skipBrowser bool
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Put the runner where af test will find it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			source, err := runnerSource(e, from)
			if err != nil {
				return err
			}
			target, err := RunnerHome()
			if err != nil {
				return aferrors.Wrap(err, aferrors.AFAGT004, "detail", err.Error())
			}

			e.Out.Section("Installing the runner")
			copied, err := copyTree(source, target)
			if err != nil {
				return aferrors.Wrap(err, aferrors.AFAGT004,
					"detail", fmt.Sprintf("copying %s to %s: %v", source, target, err))
			}
			e.Out.Printf("  %d files copied to %s\n", copied, target)

			// The dependencies and the browser are downloaded here rather than
			// on the first run, because the first run is somebody's pull
			// request check and a two minute download inside it looks like a
			// hang.
			if err := runIn(cmd.Context(), target, "npm", "install", "--no-audit", "--no-fund"); err != nil {
				return aferrors.Wrap(err, aferrors.AFAGT004,
					"detail", "npm install failed: "+err.Error())
			}
			e.Out.Println("  dependencies installed")

			browser := "skipped"
			if !skipBrowser {
				if err := runIn(cmd.Context(), target, "npx", "playwright", "install", "chromium"); err != nil {
					// Not fatal. The runner is installed and usable the moment
					// a browser arrives, and a failed download here should not
					// undo the rest.
					e.Out.Printf("  %s the browser could not be downloaded: %v\n",
						e.Out.S(StyleWarn, SymbolWarn), err)
					browser = "failed"
				} else {
					browser = "chromium"
					e.Out.Println("  chromium installed")
				}
			}

			node := nodeVersion(cmd.Context())
			if e.Out.Format == FormatJSON {
				return e.Out.JSON(RunnerInstallJSON{
					Path: target, Files: copied, Node: node,
					Browser: browser, Complete: browser == "chromium",
				})
			}
			e.Out.Printf("\n  Ready. %s will find it without a flag.\n", e.Out.S(StyleBold, "af test"))
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "Copy from this directory rather than the one beside the engine")
	cmd.Flags().BoolVar(&skipBrowser, "skip-browser", false, "Do not download the browser")
	return cmd
}

func newRunnerCheckCommand(e *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Say whether the runner can run",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			target, err := RunnerHome()
			if err != nil {
				return err
			}
			entry := filepath.Join(target, "src", "main.ts")
			_, statErr := os.Stat(entry)
			node := nodeVersion(cmd.Context())

			ready := statErr == nil && node != ""
			if e.Out.Format == FormatJSON {
				return e.Out.JSON(RunnerInstallJSON{
					Path: target, Node: node, Complete: ready,
				})
			}
			e.Out.Status(check(e, statErr == nil), "runner", target)
			e.Out.Status(check(e, node != ""), "node", orPlaceholder(node, "not found"))
			if !ready {
				e.Out.Println("")
				e.Out.Hint("Install it with", "af runner install")
				return &silentError{code: aferrors.ExitConfiguration}
			}
			return nil
		},
	}
}

func check(e *Env, ok bool) string {
	if ok {
		return e.Out.S(StyleGood, SymbolOK)
	}
	return e.Out.S(StyleBad, SymbolFail)
}

// runnerSource finds the runner to copy.
func runnerSource(e *Env, from string) (string, error) {
	candidates := []string{from}
	if from == "" {
		candidates = nil
		// Beside the working directory, for a checkout.
		candidates = append(candidates,
			filepath.Join(e.WorkDir, "runner"),
			filepath.Join(e.WorkDir, "..", "runner"))
		// Beside the binary, for an installed release, which ships the runner
		// next to it rather than fetching it.
		if self, err := os.Executable(); err == nil {
			dir := filepath.Dir(self)
			candidates = append(candidates,
				filepath.Join(dir, "runner"),
				filepath.Join(dir, "..", "share", "antifailure", "runner"))
		}
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(c, "src", "main.ts")); err == nil {
			return c, nil
		}
	}
	return "", aferrors.Coded(aferrors.AFAGT004,
		"detail", "no runner source was found; looked in "+strings.Join(candidates, ", "))
}

// copyTree copies the runner's source, and nothing it can rebuild.
func copyTree(source, target string) (int, error) {
	if err := os.MkdirAll(target, 0o755); err != nil {
		return 0, err
	}
	copied := 0
	err := filepath.WalkDir(source, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(source, path)
		if relErr != nil {
			return relErr
		}
		if d.IsDir() {
			// node_modules is reinstalled rather than copied, because it holds
			// platform specific binaries and copying one machine's to another
			// is how somebody gets a browser that will not start.
			if d.Name() == "node_modules" || d.Name() == "test-results" {
				return fs.SkipDir
			}
			if rel == "." {
				return nil
			}
			return os.MkdirAll(filepath.Join(target, rel), 0o755)
		}
		if err := copyFile(path, filepath.Join(target, rel)); err != nil {
			return err
		}
		copied++
		return nil
	})
	return copied, err
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	_, err = io.Copy(out, in)
	return err
}

func runIn(ctx context.Context, dir, name string, args ...string) error {
	c, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(c, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		tail := string(out)
		if len(tail) > 800 {
			tail = tail[len(tail)-800:]
		}
		// Wrapped, not formatted. The caller decides what to do from the
		// error underneath, and a %s here turns an exec.ExitError into a
		// string that errors.As can no longer see.
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(tail))
	}
	return nil
}

func nodeVersion(ctx context.Context) string {
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(c, "node", "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
