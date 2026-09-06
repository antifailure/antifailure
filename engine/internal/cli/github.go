package cli

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The workflow file a repository needs, and the two commands that write it.
//
// A customer's own CI file is the one piece of configuration this product
// cannot draft away: it runs Docker, Postgres and a browser against their
// application, inside their own runner, so a file in their repository has to
// say so. Everything here makes that file something they never write. `af
// init` writes it beside the manifest when the checkout is on GitHub, and `af
// github init` writes it into a project that already has a manifest.
//
// The bytes come from examples/github-workflow.yml, which the documentation
// tells people to copy, and TestTheEmbeddedWorkflowIsTheExample holds the two
// equal so the command and the page cannot drift apart.

//go:embed templates/antifailure.yml
var workflowTemplate []byte

// WorkflowPath is where the file goes, relative to the repository root.
const WorkflowPath = ".github/workflows/antifailure.yml"

// WorkflowTemplate is the file af init writes. Exported for the test that
// holds it equal to the example the documentation shows.
func WorkflowTemplate() []byte { return append([]byte(nil), workflowTemplate...) }

// workflowOutcome is what writing the file did, for the summary.
type workflowOutcome int

const (
	// workflowWritten means the file is there now and was not before, or
	// was replaced under --force.
	workflowWritten workflowOutcome = iota
	// workflowUnchanged means an identical file was already there.
	workflowUnchanged
	// workflowDiffers means a different file is there and was left alone.
	workflowDiffers
)

// writeWorkflow puts the template at root/.github/workflows/antifailure.yml.
//
// It never replaces a file that differs unless told to. A workflow somebody
// has edited is the same kind of thing as a manifest somebody has edited: the
// edits are the valuable part and nothing here can reproduce them.
func writeWorkflow(root string, force bool) (string, workflowOutcome, error) {
	path := filepath.Join(root, filepath.FromSlash(WorkflowPath))
	existing, err := os.ReadFile(path) //nolint:gosec // the path is under the repository we were pointed at
	switch {
	case err == nil && bytes.Equal(existing, workflowTemplate):
		return path, workflowUnchanged, nil
	case err == nil && !force:
		return path, workflowDiffers, nil
	case err != nil && !os.IsNotExist(err):
		return path, workflowDiffers, fmt.Errorf("github: read %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return path, workflowDiffers, fmt.Errorf("github: create %s: %w", filepath.Dir(path), err)
	}
	if err := writeAtomic(path, workflowTemplate, 0o644); err != nil {
		return path, workflowDiffers, err
	}
	return path, workflowWritten, nil
}

// githubRemote reports whether the checkout containing dir has a git remote
// on github.com.
//
// Read out of .git/config rather than asked of git, because af init runs in
// checkouts with no git on PATH and a missing binary would look like a
// missing remote. The file's grammar is simple enough: a `[remote "name"]`
// section header, then `url = ...` lines under it. A worktree's .git is a
// file pointing at the real directory, and that is followed.
//
// False on every failure. The consequence of a wrong false is that the
// workflow is not written and the summary says how to get it later, which is
// the same state as a repository with no remote and is recoverable with one
// command. The consequence of a wrong true would be a CI file in a repository
// that will never run it.
func githubRemote(dir string) bool {
	root := gitDir(dir)
	if root == "" {
		return false
	}
	body, err := os.ReadFile(filepath.Join(root, "config")) //nolint:gosec // the path is derived from the working directory
	if err != nil {
		return false
	}
	inRemote := false
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			inRemote = strings.HasPrefix(line, "[remote ")
			continue
		}
		if !inRemote {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "url" {
			continue
		}
		if isGitHubURL(strings.TrimSpace(value)) {
			return true
		}
	}
	return false
}

// isGitHubURL recognises the three spellings of a github.com remote: https,
// ssh with a scheme, and the scp style git@github.com:owner/repo.
func isGitHubURL(u string) bool {
	u = strings.ToLower(u)
	for _, prefix := range []string{
		"https://github.com/", "http://github.com/", "ssh://git@github.com/",
		"ssh://github.com/", "git@github.com:", "github.com:", "git://github.com/",
	} {
		if strings.HasPrefix(u, prefix) {
			return true
		}
	}
	return false
}

// gitDir finds the .git directory for dir, walking up and following a
// worktree's pointer file. Empty when there is none.
func gitDir(dir string) string {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		candidate := filepath.Join(dir, ".git")
		info, statErr := os.Stat(candidate)
		if statErr == nil {
			if info.IsDir() {
				return candidate
			}
			// A worktree or a submodule: a file reading "gitdir: <path>".
			// The common config lives in the parent repository's directory,
			// which for a worktree is two levels above the gitdir named.
			body, readErr := os.ReadFile(candidate) //nolint:gosec // found by walking up from the working directory
			if readErr != nil {
				return ""
			}
			target := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(body)), "gitdir:"))
			if target == "" {
				return ""
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(dir, target)
			}
			if _, err := os.Stat(filepath.Join(target, "config")); err == nil {
				return target
			}
			if common, err := os.ReadFile(filepath.Join(target, "commondir")); err == nil { //nolint:gosec // same
				c := strings.TrimSpace(string(common))
				if !filepath.IsAbs(c) {
					c = filepath.Join(target, c)
				}
				return c
			}
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// gitWorkTree is the directory the .git entry sits in, which is where the
// workflow file belongs. Falls back to dir when there is no repository, so a
// caller that already decided to write still has somewhere to write.
func gitWorkTree(dir string) string {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		if filepath.Dir(d) == d {
			return dir
		}
	}
}

// draftGitHub is the block af init adds when it writes the workflow: the
// three settings the file relies on, stated in the manifest so that `af
// explain` shows them and the fork gate reads them from a line somebody can
// see rather than from a default.
func draftGitHub() *schema.GitHub {
	comment := true
	return &schema.GitHub{
		Mode: schema.GitHubActions, Comment: &comment, ForkPolicy: schema.ForkLabel,
	}
}

// GitHubInitReport is the JSON form of af github init.
type GitHubInitReport struct {
	WorkflowPath string `json:"workflow_path"`
	// Written is whether the file is new or was replaced. Unchanged means it
	// was already the template, and Differs means a different file was left
	// alone.
	Written    bool     `json:"written"`
	Unchanged  bool     `json:"unchanged"`
	Differs    bool     `json:"differs"`
	Manifest   string   `json:"manifest_path"`
	Configured bool     `json:"manifest_updated"`
	Secrets    []string `json:"secrets"`
	Variables  []string `json:"variables"`
}

func newGitHubCommand(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "github",
		Short: "Connect this repository's pull requests to Antifailure",
		Long: strings.TrimSpace(`
The pull request integration runs in the repository's own GitHub Actions,
because an environment needs Docker, Postgres and a browser beside the code
under test, and the masked data stays inside the customer's own runner.

One workflow file makes that happen, and these commands write it.`),
	}
	cmd.AddCommand(newGitHubInitCommand(env))
	return cmd
}

func newGitHubInitCommand(env *Env) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write the workflow that checks every pull request",
		Long: strings.TrimSpace(`
Writes .github/workflows/antifailure.yml, the same file af init writes when
the checkout has a github.com remote, into a repository that already has a
manifest. The file calls a reusable workflow in the antifailure repository, so
it is short and rarely needs to change.

It is safe to run twice. A file identical to the template is left as it is and
said to be. A file that differs is left alone unless --force replaces it,
because a workflow somebody edited is theirs.

The manifest gains a github block when it has none, naming the three settings
the file depends on: the mode, whether a comment is left, and the fork policy.

Every secret the check can use is optional and is printed here by name, never
by value. The one repository variable a hosted control plane needs is printed
the same way.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGitHubInit(cmd.Context(), env, force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false,
		"Replace a workflow file that differs from the template")
	return cmd
}

func runGitHubInit(_ context.Context, env *Env, force bool) error {
	path, err := manifest.Find(env.WorkDir)
	if err != nil {
		return err
	}
	m, err := manifest.Load(path)
	if err != nil {
		return err
	}
	root := repoRoot(path)

	wfPath, outcome, err := writeWorkflow(root, force)
	if err != nil {
		return err
	}

	configured, err := ensureGitHubBlock(path, root)
	if err != nil {
		return err
	}

	rep := GitHubInitReport{
		WorkflowPath: wfPath, Manifest: path, Configured: configured,
		Written:   outcome == workflowWritten,
		Unchanged: outcome == workflowUnchanged,
		Differs:   outcome == workflowDiffers,
		Secrets:   optionalSecrets(m),
		Variables: []string{"AF_CONTROL_PLANE"},
	}
	if env.Out.Format == FormatJSON {
		return env.Out.JSON(rep)
	}

	rel := short(env.WorkDir, wfPath)
	switch outcome {
	case workflowWritten:
		env.Out.Section("Written")
		env.Out.Printf("  %s\n", rel)
	case workflowUnchanged:
		env.Out.Section("Already there")
		env.Out.Printf("  %s is the template, so nothing was changed.\n", rel)
	case workflowDiffers:
		env.Out.Section("Left alone")
		env.Out.Printf("  %s\n", rel)
		env.Out.Note(StyleWarn, "That file differs from the template, so it was not touched. "+
			"Pass --force to replace it with the template.")
	}
	if configured {
		env.Out.Printf("  %s gained a github block.\n", short(env.WorkDir, path))
	}

	env.Out.Section("Repository secrets, all optional")
	env.Out.Println(env.Out.Wrap("None of these is needed for the check to run. Each one "+
		"turns on a part of it, and the report says which parts ran without one.", 2))
	block := env.Out.Block()
	for _, name := range rep.Secrets {
		block.Add(name, secretPurpose(name, m))
	}
	block.Flush()

	env.Out.Section("Repository variable, only for a self hosted control plane")
	block = env.Out.Block()
	block.Add("AF_CONTROL_PLANE", "where the run reports. The file carries the hosted control "+
		"plane's address as the default, so leave it unset unless the run should report to a "+
		"control plane of your own, which then keeps the check and the comment")
	block.Flush()
	env.Out.Println("")
	env.Out.Hint("Commit the file, open a pull request, and read the comment. Then", "af start")
	return nil
}

// ensureGitHubBlock appends a github block to the manifest when it has none,
// and reports whether it did.
//
// Appended as text rather than re-encoded, because the encoder would drop
// every comment in the file, and the comments are where af init explains
// which values were guesses. The result is parsed before it is written, the
// way af init parses its own draft, so a block that broke the document fails
// the command rather than reaching the file.
func ensureGitHubBlock(path, root string) (bool, error) {
	body, err := os.ReadFile(path) //nolint:gosec // the manifest we already loaded
	if err != nil {
		return false, fmt.Errorf("github: read %s: %w", path, err)
	}
	var raw struct {
		GitHub any `yaml:"github"`
	}
	if err := yaml.Unmarshal(body, &raw); err == nil && raw.GitHub != nil {
		return false, nil
	}
	var b strings.Builder
	b.Write(body)
	if len(body) > 0 && !bytes.HasSuffix(body, []byte("\n")) {
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(githubBlockText())
	if _, err := manifest.Parse([]byte(b.String()), path, root); err != nil {
		return false, aferrors.Coded(aferrors.AFGH004, "path", path, "detail", validationDetail(err))
	}
	if err := writeAtomic(path, []byte(b.String()), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// githubBlockText is the block, with the sentence a reader needs above it.
func githubBlockText() string {
	return strings.TrimSpace(`
# Written by 'af github init'. The workflow at .github/workflows/antifailure.yml
# reads these: it runs in GitHub Actions, leaves one comment per pull request
# that it edits in place, and a pull request from a fork waits for the
# 'antifailure:allow' label before anything runs.
github:
  mode: actions
  comment: true
  fork_policy: label
`) + "\n"
}

// optionalSecrets names every repository secret the check can use for this
// manifest. Names only. The value of any of them never passes through here.
func optionalSecrets(m *schema.Manifest) []string {
	names := []string{"ANTHROPIC_API_KEY", "AF_MASKING_KEY"}
	if m != nil && m.Database != nil && m.Database.SourceURLEnv != "" {
		names = append(names, m.Database.SourceURLEnv)
	}
	if m != nil && m.Egress != nil {
		for _, r := range m.Egress.Rules {
			if r.Mode == schema.ModeSandbox && strings.Contains(strings.ToLower(r.Host), "stripe") {
				names = append(names, "STRIPE_TEST_SECRET_KEY")
				break
			}
		}
	}
	return names
}

// secretPurpose is the one line beside each secret's name.
func secretPurpose(name string, m *schema.Manifest) string {
	switch {
	case name == "ANTHROPIC_API_KEY":
		return "lets the agents drive the workflows; without it the workflows are reported unverified"
	case name == "AF_MASKING_KEY":
		return "keeps masked values stable between goldens; without it each refresh masks afresh"
	case name == "STRIPE_TEST_SECRET_KEY":
		return "the test mode key the sandbox rule sends instead of the live one"
	case m != nil && m.Database != nil && name == m.Database.SourceURLEnv:
		return "the read only connection string the golden is masked from; without it the database starts empty"
	}
	return ""
}
