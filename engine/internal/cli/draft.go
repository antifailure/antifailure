package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/detect"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// A manifest drafted in memory, for the commands that run with none.
//
// `af ci` and `af change` used to need antifailure.yaml, so a repository that
// had only the workflow file got AF-MAN-001 on its first pull request and no
// check. The whole point of the workflow being something a customer never
// writes is lost if the next thing they meet is a file they have to write. So
// with no manifest these commands draft one the way `af init` would, take
// every default, and say so at the top of the report.
//
// The draft is never written. A file that appears in a checkout because a CI
// job ran is a file nobody committed and nobody can explain, and the report's
// first line already tells the reader which command writes it on purpose.

// draft is what drafting produced, and what it had to decide on the way.
type draft struct {
	Manifest *schema.Manifest
	// Root is the directory the draft describes, which is where the manifest
	// would have been.
	Root string
	// Assumed is every value nothing in the repository decided, by question
	// id, the same map af init prints under Assumed.
	Assumed map[string]string
	// Dropped names the services that could not be drafted at all, with the
	// reason. A service with no start command and no conventional one is
	// dropped rather than failing the run, because a check on the rest of
	// the application is worth more than no check.
	Dropped []string
}

// notes renders what the draft had to decide, one sentence each, for a
// report's "Not measured" lines.
func (d *draft) notes() []string {
	var out []string
	for _, id := range SortedKeys(d.Assumed) {
		out = append(out, "the drafted manifest assumed "+id+": "+d.Assumed[id])
	}
	out = append(out, d.Dropped...)
	return out
}

// draftManifest reads the repository and drafts a manifest with every
// question answered by its default.
//
// Where a question has no default the service it is about is dropped and
// named, rather than the whole run refusing. That is a different answer from
// the one `af init --non-interactive` gives, and deliberately: `af init` is
// somebody at a keyboard who can pass --answer, and a refusal there costs one
// more command. A pull request check has nobody at the keyboard, and a refusal
// costs the check.
func draftManifest(ctx context.Context, e *Env, root string) (*draft, error) {
	res, err := detect.Run(ctx, os.DirFS(root), root, detect.Options{Clock: e.Clock})
	if err != nil {
		return nil, err
	}
	if len(res.Draft.Services) == 0 {
		return nil, aferrors.Coded(aferrors.AFDET001, "path", root)
	}
	d := &draft{Manifest: res.Draft, Root: root, Assumed: assumedByConstruction(res.Draft)}

	dropped := map[string]bool{}
	for _, q := range res.Questions {
		switch {
		case q.Migration != "":
			if q.Default != "" {
				applyMigrationAnswer(res.Draft, q, q.Default)
				d.Assumed[q.ID] = q.Default
				continue
			}
			d.Dropped = append(d.Dropped, "the migration command "+q.Migration+
				" was found outside any one service, so no service runs it in this draft")
		case q.Default != "":
			// The default is the answer. Recorded either way, because a
			// default that was applied and a default that reached nothing
			// both belong under Assumed: the second is a defect worth seeing.
			_ = applyAnswer(res.Draft, q.ID, q.Default)
			d.Assumed[q.ID] = q.Default
		default:
			name := serviceOfQuestion(q.ID)
			if name == "" || dropped[name] {
				continue
			}
			dropped[name] = true
			d.Dropped = append(d.Dropped, fmt.Sprintf(
				"the service %s was left out of the drafted manifest, because %s "+
					"Answer it in antifailure.yaml with af init.",
				name, strings.ToLower(q.Prompt[:1])+q.Prompt[1:]))
		}
	}
	if len(dropped) > 0 {
		dropServices(res.Draft, dropped)
	}
	if len(res.Draft.Services) == 0 {
		return nil, aferrors.Coded(aferrors.AFDET001, "path", root)
	}
	sort.Strings(d.Dropped)

	// Normalised and validated exactly the way af init does before it writes,
	// so a draft that would have been refused as a file is refused here too
	// rather than reaching an orchestrator.
	body, err := renderManifest(res.Draft)
	if err != nil {
		return nil, err
	}
	m, err := manifest.Parse(body, filepath.Join(root, manifest.FileName)+" (drafted)", root)
	if err != nil {
		return nil, aferrors.Coded(aferrors.AFDET005,
			"path", filepath.Join(root, manifest.FileName), "detail", validationDetail(err))
	}
	d.Manifest = m
	return d, nil
}

// applyMigrationAnswer puts the migration command on the named service.
// Shared with resolveQuestions so the two cannot apply an answer differently.
func applyMigrationAnswer(m *schema.Manifest, q detect.Question, answer string) bool {
	for j := range m.Services {
		if m.Services[j].Name == answer && m.Services[j].Migrate == "" {
			m.Services[j].Migrate = q.Migration
			return true
		}
	}
	return false
}

// serviceOfQuestion is the service a question id is about, or empty for a
// question about something else.
func serviceOfQuestion(id string) string {
	if !strings.HasPrefix(id, "service.") {
		return ""
	}
	rest := strings.TrimPrefix(id, "service.")
	i := strings.LastIndexByte(rest, '.')
	if i <= 0 {
		return ""
	}
	return rest[:i]
}

// dropServices removes the named services and every reference to them, so the
// draft still validates.
func dropServices(m *schema.Manifest, gone map[string]bool) {
	kept := m.Services[:0]
	for _, s := range m.Services {
		if gone[s.Name] {
			continue
		}
		var deps []string
		for _, d := range s.DependsOn {
			if !gone[d] {
				deps = append(deps, d)
			}
		}
		s.DependsOn = deps
		kept = append(kept, s)
	}
	m.Services = kept
}

// isNoManifest reports whether an error is AF-MAN-001, the one absence these
// commands draft their way past. Any other failure loading a manifest stays an
// error, because a manifest that exists and does not parse must not quietly
// become no manifest.
func isNoManifest(err error) bool {
	var coded *aferrors.Error
	return aferrors.As(err, &coded) && coded.Entry.Code == aferrors.AFMAN001
}
