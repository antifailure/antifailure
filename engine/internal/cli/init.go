package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/antifailure/antifailure/engine/internal/detect"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/internal/state"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

func newInitCommand(env *Env) *cobra.Command {
	var (
		nonInteractive bool
		force          bool
		answers        []string
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Read the repository and write antifailure.yaml",
		Long: strings.TrimSpace(`
Detection reads the repository and proposes a manifest: the services it found,
the port each listens on, the migration command, and, most usefully, a network
policy derived from the SDKs you depend on.

It never runs anything from the repository. Everything it reports names the
file it came from, so you can check the reasoning rather than trust it.

Anything detection is not sure about becomes a question rather than a silent
guess, because a manifest you have to audit is worth less than one you can
read.

A service is identified by the directory it is built and run from, not by its
name, because every source spells the name differently: a Dockerfile and a
language analyzer use the directory, a compose file uses its own key, a
Procfile uses the process name, and a package manifest uses the package. One
application described by several of those is one service, and the name it keeps
comes from the source that identifies an application best, a package manifest
ahead of a compose key ahead of a Procfile process ahead of the directory.
Where one source declares two services in a directory, which is what a compose
file with a web and an admin container on one build context is, they stay two.

A Dockerfile in a subdirectory is built either from that directory, which is
what 'docker build <dir>' does, or from the repository root, which is what a
monorepo image reaching a lockfile at the top of the tree needs. Its COPY lines
say which: a path that exists beside the Dockerfile and not at the root means
the directory, and one that exists only at the root means the root. Where they
do not settle it, this is a question rather than a default, because building
from the wrong one either fails on a missing path or, with COPY . ., succeeds
and produces an image assembled from the wrong directory.

--answer settles a question, and also overrides a value detection read with
confidence, which is how you separate two services a repository really does
have on one port. An id naming nothing is refused with the ids that would have
worked rather than dropped in silence.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runInit(cmd.Context(), env, initOptions{
				nonInteractive: nonInteractive || env.Out.Format == FormatJSON,
				force:          force,
				answers:        parseAnswers(answers),
			})
		},
	}
	cmd.Flags().BoolVar(&nonInteractive, "non-interactive", false,
		"Do not ask questions; accept every default and report what was assumed")
	cmd.Flags().BoolVar(&force, "force", false,
		"Overwrite an existing manifest instead of merging into it")
	cmd.Flags().StringArrayVar(&answers, "answer", nil,
		"Answer a question, or override a detected value, as id=value. Repeatable.")
	return cmd
}

type initOptions struct {
	nonInteractive bool
	force          bool
	answers        map[string]string
}

func parseAnswers(raw []string) map[string]string {
	out := map[string]string{}
	for _, a := range raw {
		if i := strings.IndexByte(a, '='); i > 0 {
			out[a[:i]] = a[i+1:]
		}
	}
	return out
}

// InitReport is the JSON form of af init.
type InitReport struct {
	ManifestPath     string            `json:"manifest_path"`
	Created          bool              `json:"created"`
	Services         []string          `json:"services"`
	EgressRules      int               `json:"egress_rules"`
	Questions        []detect.Question `json:"questions,omitempty"`
	Assumed          map[string]string `json:"assumed,omitempty"`
	Findings         int               `json:"findings"`
	Partial          bool              `json:"partial"`
	UnassignedImages []string          `json:"unassigned_images,omitempty"`
}

func runInit(ctx context.Context, env *Env, opts initOptions) error {
	manifestPath := filepath.Join(env.WorkDir, manifest.FileName)

	// An existing manifest is never silently replaced. A user's edits are the
	// most valuable thing in the file, and detection cannot reproduce them.
	if _, err := os.Stat(manifestPath); err == nil && !opts.force {
		return aferrors.Coded(aferrors.AFMAN002,
			"path", manifestPath,
			"detail", "a manifest already exists, and af init would overwrite the edits in it")
	}

	env.Out.Printf("Reading %s\n", env.WorkDir)
	res, err := detect.Run(ctx, os.DirFS(env.WorkDir), env.WorkDir, detect.Options{Clock: env.Clock})
	if err != nil {
		return err
	}

	if len(res.Draft.Services) == 0 {
		return aferrors.Coded(aferrors.AFDET001, "path", env.WorkDir)
	}

	// assumed was fed only by resolveQuestions, so a guess that never became a
	// question could not be disclosed at all. That is why the one thing this
	// command admitted to guessing was database.present: personas and workflows
	// are written unconditionally and are never asked about, so the mechanism
	// built to catch exactly this could not see them. Seeded first, then the
	// questions add to it.
	assumed := assumedByConstruction(res.Draft)
	if len(res.Questions) > 0 {
		if err := resolveQuestions(env, res, opts, assumed); err != nil {
			return err
		}
	}
	if err := applyRemainingAnswers(res, opts); err != nil {
		return err
	}

	// The draft is normalized and validated before it is written, so af init
	// can never produce a file that af up then refuses.
	body, err := renderManifest(res.Draft)
	if err != nil {
		return err
	}
	for _, q := range res.Questions {
		if q.Migration != "" && assumed[q.ID] != "" {
			body = append([]byte("# "+strings.ReplaceAll(assumed[q.ID], "\n", "\n# ")+"\n"), body...)
		}
	}
	if _, err := manifest.Parse(body, manifestPath, env.WorkDir); err != nil {
		// The refusal has to say that nothing was written. Wrapping the
		// validator's own error let AF-MAN-002 reach the user unchanged, and
		// its next step is "fix the reported line", which points at a file
		// that does not exist. Writing the invalid draft instead would be
		// worse: it gets committed, every later command fails on it, and
		// af init then refuses to replace it without --force.
		return aferrors.Coded(aferrors.AFDET005,
			"path", manifestPath,
			"detail", validationDetail(err))
	}

	if err := writeAtomic(manifestPath, body, 0o644); err != nil {
		return err
	}
	if err := ensureStateIgnored(env.WorkDir); err != nil {
		return err
	}
	if err := writeStateReadme(env.WorkDir); err != nil {
		return err
	}

	names := make([]string, 0, len(res.Draft.Services))
	for _, s := range res.Draft.Services {
		names = append(names, s.Name)
	}
	report := InitReport{
		ManifestPath: manifestPath, Created: true, Services: names,
		EgressRules: len(res.Draft.Egress.Rules), Questions: res.Questions,
		UnassignedImages: res.UnassignedImages,
		Assumed:          assumed, Findings: len(res.Findings), Partial: res.Partial,
	}
	if env.Out.Format == FormatJSON {
		return env.Out.JSON(report)
	}
	renderInitSummary(env, res, assumed, manifestPath)
	return nil
}

// validationDetail pulls the readable half out of the validator's own error.
//
// The validator returns AF-MAN-002 carrying the failing line and the reason.
// That sentence is worth repeating; its next step is not, because it tells the
// reader to edit a file 'af init' has just decided not to write.
func validationDetail(err error) string {
	var coded *aferrors.Error
	if aferrors.As(err, &coded) {
		if d := coded.Fields["detail"]; d != "" {
			return d
		}
		return coded.Message()
	}
	return err.Error()
}

// assumedByConstruction names the parts of the draft that no file decided.
//
// Written from what merge.go actually does rather than from what it ought to:
// defaultPersonas returns the same two accounts for every repository, and
// suggestedWorkflows always emits sign-up whatever the dependencies say. Both
// are useful starting points and neither is a detection result, so both belong
// here, where the command already tells the reader what to check.
func assumedByConstruction(draft *schema.Manifest) map[string]string {
	out := map[string]string{}
	if draft == nil {
		return out
	}
	if n := len(draft.Personas); n > 0 {
		out["personas"] = fmt.Sprintf(
			"%s written for every project, not read from this one", plural(n, "account", "accounts"))
	}
	if n := len(draft.Workflows); n > 0 {
		out["workflows"] = fmt.Sprintf(
			"%s guessed from the dependencies, describing what an application like this usually does",
			plural(n, "workflow", "workflows"))
	}
	return out
}

func resolveQuestions(env *Env, res *detect.Result, opts initOptions, assumed map[string]string) error {
	// A question needs somewhere to ask it. Without a terminal the read blocks
	// forever, which in a script or a CI job looks exactly like a hang, so the
	// refusal has to happen before the first prompt rather than at it.
	if !opts.nonInteractive && !env.Interactive() {
		return aferrors.Coded(aferrors.AFMAN004, "path", env.WorkDir)
	}
	for i := range res.Questions {
		q := &res.Questions[i]
		answer, given := opts.answers[q.ID]
		switch {
		case given:
		case opts.nonInteractive:
			answer = q.Default
			if answer != "" {
				assumed[q.ID] = answer
			}
		default:
			var err error
			answer, err = ask(env, *q)
			if err != nil {
				return err
			}
		}
		// One refusal for every way a question can arrive unanswered: a
		// non interactive run with no default, an --answer with nothing after
		// the equals sign, and a prompt somebody pressed return on when there
		// was no default to take.
		//
		// Letting any of them through applied no answer, which left a web
		// service with no port, which failed validation and told the user it
		// was a defect in Antifailure. Two of these used to return AF-MAN-004,
		// whose next step is "pass --non-interactive", which the run that hit
		// it had already done. AF-DET-004 names the question and the flag that
		// answers it instead.
		if answer == "" && q.Default == "" {
			return aferrors.Coded(aferrors.AFDET004,
				"question", q.Prompt,
				"id", q.ID)
		}
		if q.Migration != "" {
			if answer == "manual:configure" {
				assumed[q.ID] = "Not configured: " + q.Migration + ". Configure migrations before af up."
				continue
			}
			applied := false
			for j := range res.Draft.Services {
				if res.Draft.Services[j].Name == answer && res.Draft.Services[j].Migrate == "" {
					res.Draft.Services[j].Migrate = q.Migration
					applied = true
				}
			}
			if !applied {
				return aferrors.Coded(aferrors.AFDET006, "id", q.ID+"="+answer, "known", strings.Join(q.Options, ", "))
			}
			continue
		}
		_ = applyAnswer(res.Draft, q.ID, answer)
	}
	return nil
}

// applyRemainingAnswers applies every --answer detection did not turn into a
// question, and refuses one that reaches nothing.
//
// Detection only asks about what it is unsure of, so a value it read with
// confidence has no question and used to be unreachable from the command line.
// That made AF-DET-005 a dead end of the same shape as the ones this change
// exists to close: two Dockerfiles in different directories both exposing 3000
// is a real repository, the draft is correctly refused, and the remedy the
// refusal named did nothing at all because there was no question to answer.
func applyRemainingAnswers(res *detect.Result, opts initOptions) error {
	answered := map[string]bool{}
	for _, q := range res.Questions {
		answered[q.ID] = true
	}
	ids := make([]string, 0, len(opts.answers))
	for id := range opts.answers {
		if !answered[id] {
			ids = append(ids, id)
		}
	}
	// The map iterates in a random order and this can refuse, so sort it or
	// which id a repository with two bad answers is told about is a coin flip.
	sort.Strings(ids)
	for _, id := range ids {
		if !applyAnswer(res.Draft, id, opts.answers[id]) {
			return aferrors.Coded(aferrors.AFDET006,
				"id", id,
				"known", strings.Join(answerIDs(res.Draft), ", "))
		}
	}
	return nil
}

// ask prompts for one question on the terminal.
func ask(env *Env, q detect.Question) (string, error) {
	env.Out.Println("")
	env.Out.Printf("  %s\n", env.Out.S(StyleBold, q.Prompt))
	if q.Why != "" {
		env.Out.Printf("  %s\n", env.Out.S(StyleDim, q.Why))
	}
	if len(q.Options) > 0 {
		env.Out.Printf("  %s\n", env.Out.S(StyleDim, "Options: "+strings.Join(q.Options, ", ")))
	}
	prompt := "  > "
	if q.Default != "" {
		prompt = fmt.Sprintf("  [%s] > ", q.Default)
	}
	env.Out.Raw(prompt)

	var line string
	if _, err := fmt.Fscanln(env.Stdin, &line); err != nil {
		// An empty line reads as an error from Fscanln, and an empty line
		// means "take the default", which is the most common answer.
		line = ""
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return q.Default, nil
	}
	return line, nil
}

// applyAnswer writes one answer into the draft.
// It reports whether the answer reached anything. A caller that passed an id
// naming no service has to hear about it: an --answer silently discarded is
// the same dead end as an error naming a remedy that does nothing, one layer
// further in, and AF-DET-005 tells people to reach for this flag.
func applyAnswer(m *schema.Manifest, id, answer string) bool {
	if answer == "" {
		return false
	}
	switch {
	case strings.HasPrefix(id, "service.") && strings.HasSuffix(id, ".port"):
		name := strings.TrimSuffix(strings.TrimPrefix(id, "service."), ".port")
		port, err := strconv.Atoi(answer)
		if err != nil || port <= 0 || port > 65535 {
			return false
		}
		applied := false
		for i := range m.Services {
			if m.Services[i].Name == name {
				m.Services[i].Port = port
				applied = true
			}
		}
		return applied
	case strings.HasPrefix(id, "service.") && strings.HasSuffix(id, ".command"):
		name := strings.TrimSuffix(strings.TrimPrefix(id, "service."), ".command")
		applied := false
		for i := range m.Services {
			if m.Services[i].Name == name {
				m.Services[i].Command = answer
				applied = true
			}
		}
		return applied
	case strings.HasPrefix(id, "service.") && strings.HasSuffix(id, ".context"):
		name := strings.TrimSuffix(strings.TrimPrefix(id, "service."), ".context")
		applied := false
		for i := range m.Services {
			if m.Services[i].Name != name || m.Services[i].Build == nil {
				continue
			}
			// "." is the repository root, which is what an unset context
			// already means. Writing it would put a value in the manifest
			// that changes nothing, and a reader would reasonably expect it
			// to mean something.
			if answer == "." {
				m.Services[i].Build.Context = ""
			} else {
				m.Services[i].Build.Context = answer
			}
			applied = true
		}
		return applied
	case id == "database.present":
		if strings.EqualFold(answer, "no") {
			m.Database = &schema.Database{Provider: schema.DBDocker, Version: 17}
		}
		return true
	}
	return false
}

// answerIDs lists every id --answer accepts for this repository, so a refusal
// can name them rather than referring the reader to the manual.
func answerIDs(m *schema.Manifest) []string {
	ids := []string{"database.present"}
	for _, s := range m.Services {
		ids = append(ids, "service."+s.Name+".port", "service."+s.Name+".command")
		if s.Build != nil && s.Build.Strategy == schema.BuildDockerfile {
			ids = append(ids, "service."+s.Name+".context")
		}
	}
	return ids
}

func renderInitSummary(env *Env, res *detect.Result, assumed map[string]string, path string) {
	env.Out.Section("Detected")
	rows := make([][]string, 0, len(res.Draft.Services))
	for _, s := range res.Draft.Services {
		port := ""
		if s.Port != 0 {
			port = strconv.Itoa(s.Port)
		}
		where := s.Path
		if where == "" {
			where = "."
		}
		rows = append(rows, []string{s.Name, string(s.Kind), port, where, s.Command})
	}
	env.Out.Table([]Column{
		Col("SERVICE"), Col("KIND"), Num("PORT"), Col("PATH"), Flex("COMMAND"),
	}, rows)
	if len(res.UnassignedImages) > 0 {
		env.Out.Section("Images without a detected service")
		for _, dockerfile := range res.UnassignedImages {
			env.Out.Printf("  %s\n", dockerfile)
		}
		env.Out.Note(StyleWarn, "These images were not added. No command or port established their runtime; a base image may supply one. If an image runs part of your application, declare that service and its command in Compose or in this manifest before af up.")
	}

	if len(res.Draft.Egress.Rules) > 0 {
		env.Out.Section("Network policy")
		ruleRows := make([][]string, 0, len(res.Draft.Egress.Rules))
		for _, r := range res.Draft.Egress.Rules {
			ruleRows = append(ruleRows, []string{r.Host, string(r.Mode), r.Note})
		}
		env.Out.Table([]Column{Col("HOST"), Col("MODE"), Flex("WHY")}, ruleRows)
		env.Out.Note(StyleDim,
			"Everything not listed is blocked. Nothing reaches the internet by accident.")
	}

	if len(assumed) > 0 {
		env.Out.Section("Assumed")
		block := env.Out.Block()
		for _, id := range SortedKeys(assumed) {
			block.Add(id, assumed[id])
		}
		block.Flush()
		env.Out.Note(StyleDim,
			"These were not detected with confidence. Check them before you commit.")
	}
	if res.Partial {
		env.Out.Note(StyleWarn,
			"Detection did not finish within its budget, so the draft may be incomplete.")
	}

	env.Out.Section("Written")
	env.Out.Printf("  %s\n", path)
	env.Out.Println("")
	env.Out.Hint("Read it, edit anything that looks wrong, then run", "af up")
}

// renderManifest writes the draft as YAML with a header that explains what the
// file is.
//
// The header matters more than it looks. This file gets committed and read by
// people who did not run the command, and the first thing they need to know is
// that everything not listed under egress is blocked.
func renderManifest(m *schema.Manifest) ([]byte, error) {
	var b strings.Builder
	b.WriteString("# Antifailure manifest. https://antifailure.dev/docs/reference/manifest\n")
	b.WriteString("#\n")
	// The old sentence read "Every value here came from a file: a package
	// manifest, a Dockerfile, a compose file, or a dependency list."
	//
	// That was false, and falsest exactly where it mattered. personas and
	// workflows are written unconditionally and are derived from nothing:
	// two accounts at example.test and a sign-up workflow describing a form,
	// handed to a JSON API that has neither. So a reader was told the file
	// described their repository, ran the two commands the tool printed, and
	// the second one failed on a table their application does not have.
	//
	// For a product whose whole claim is evidence rather than assertion, its
	// first command writing a file that misstates where its own values came
	// from is the worst defect available. The sentence is a claim, and this
	// project gates claims. It says what is true now, and everything it does
	// not cover is marked where it appears.
	b.WriteString("# Generated by 'af init'. The services, ports, build settings and network\n")
	b.WriteString("# policy below were read out of files in this repository: a package\n")
	b.WriteString("# manifest, a Dockerfile, a compose file, or a dependency list. Anything\n")
	b.WriteString("# marked as a starting point was not, and is a guess for you to correct.\n")
	b.WriteString("# Edit it freely; nothing regenerates it behind your back.\n")
	b.WriteString("#\n")
	b.WriteString("# The rule worth knowing before you read further: an environment can reach\n")
	b.WriteString("# nothing on the network except the hosts listed under egress, and each of\n")
	b.WriteString("# those in the mode named. Everything else is refused with a decision you\n")
	b.WriteString("# can read.\n\n")

	// A two space indent, written through an Encoder rather than Marshal,
	// because Marshal's four space default does not match a single hand
	// written YAML file in this ecosystem and the result reads as generated.
	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(m); err != nil {
		return nil, fmt.Errorf("init: render the manifest: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("init: render the manifest: %w", err)
	}
	body := buf.String()

	// Marked where they appear, not only in the summary the command prints
	// once. The manifest is committed and read later by people who never ran
	// af init, and for them the printed summary does not exist.
	body = noteBefore(body, "personas", []string{
		"Starting points, not detected. Nothing in a repository says who its users are,",
		"so these two are written for every project: an admin and an ordinary member.",
		"Change the addresses and roles to ones your application would accept.",
		"A persona that never signs in wants 'login: none', and then no account is",
		"created and no users table has to exist.",
	})
	body = noteBefore(body, "workflows", []string{
		"Starting points, not detected. These are guessed from the dependencies above,",
		"so they describe what an application like this usually does rather than what",
		"yours does. Read each one as a sentence and rewrite it to match a real task,",
		"or delete it. af test refuses a manifest with none rather than reporting a run",
		"that examined nothing.",
	})
	b.WriteString(body)

	if m.Database != nil && m.Database.SourceURLEnv == "" {
		b.WriteString("\n")
		b.WriteString(strings.TrimSpace(`
# Detection found no production database to build a golden from, so branches
# start from an empty database this project makes for itself. A golden another
# project on this machine made is never branched into an environment here,
# whatever the two have in common. To copy production instead, add the name of
# the variable holding its read only connection string:
#
#   database:
#     source_url_env: PRODUCTION_DATABASE_URL
#
# The value is read once, during a golden refresh, on your machine or your CI
# runner. It is never stored, and no environment ever receives it.
`))
		b.WriteString("\n")
	}
	return []byte(b.String()), nil
}

// noteBefore puts a comment block above a top level key.
//
// A string operation on encoded YAML rather than a comment carried on the
// struct, because the encoder in use has no way to attach one and threading a
// comment field through schema.Manifest would put a presentation concern in the
// type the engine validates. It is anchored on a key at column zero, which is
// what a top level key is, and a key that is not there leaves the document
// untouched: a manifest with no personas gets no note about personas.
//
// The guarantee that matters is not the anchor, it is that runInit parses the
// rendered bytes back before writing them, so a note that broke the document
// fails the command rather than reaching a file.
func noteBefore(body, key string, lines []string) string {
	anchor := "\n" + key + ":\n"
	i := strings.Index(body, anchor)
	if i < 0 {
		if !strings.HasPrefix(body, key+":\n") {
			return body
		}
		i = -1
	}
	// Two newlines: one closes the previous block and one leaves a blank line,
	// so the note reads as its own paragraph rather than as a trailing comment
	// on whatever happened to be encoded above it.
	var note strings.Builder
	note.WriteString("\n\n")
	for _, l := range lines {
		note.WriteString("# " + l + "\n")
	}
	if i < 0 {
		return strings.TrimPrefix(note.String(), "\n") + body
	}
	return body[:i] + note.String() + body[i+1:]
}

// writeAtomic writes through a temporary file and a rename, so an interrupted
// write leaves the previous file rather than a truncated one.
func writeAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".af-*")
	if err != nil {
		return fmt.Errorf("init: create a temporary file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("init: write %s: %w", tmpName, err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("init: set permissions on %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("init: close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("init: replace %s: %w", path, err)
	}
	return nil
}

// ensureStateIgnored adds the state directory to .gitignore.
//
// The directory holds the journal and local handles. Committing it would put
// one developer's environment identifiers into everyone else's checkout, and
// the journal would then describe resources that do not exist on their machine.
func ensureStateIgnored(root string) error {
	path := filepath.Join(root, ".gitignore")
	entry := state.DirName + "/"

	existing, err := os.ReadFile(path) //nolint:gosec // the path is the repository we were pointed at
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("init: read %s: %w", path, err)
	}
	for _, l := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(l) == entry || strings.TrimSpace(l) == state.DirName {
			return nil
		}
	}
	var b strings.Builder
	b.Write(existing)
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		b.WriteString("\n")
	}
	b.WriteString("\n# Antifailure local state: the journal and local handles. Never commit it.\n")
	b.WriteString(entry + "\n")
	return writeAtomic(path, []byte(b.String()), 0o644)
}

func writeStateReadme(root string) error {
	dir := filepath.Join(root, state.DirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("init: create %s: %w", dir, err)
	}
	readme := strings.TrimSpace(`
This directory is local state, and it is not committed.

It holds the journal, which records every external resource an environment
created before it was created, so that a crash at any point leaves something
'af down' can clean up. It also holds environment records, masking checkpoints
so an interrupted run resumes rather than restarts, and the event log.

It never holds a secret, and it never holds customer data.

Deleting it is safe but not free: the journal is how teardown knows what to
remove. If you delete it while an environment is running, run
'af env prune --older-than 0' afterwards: it inventories the provider rather
than reading the journal, so it can still find what the journal no longer
names.
`) + "\n"
	return writeAtomic(filepath.Join(dir, "README.md"), []byte(readme), 0o600)
}

var _ = sort.Strings
