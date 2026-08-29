package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tree writes a repository shaped enough for the checker: a manifest schema
// and one documentation page.
func tree(t *testing.T, page string, exemptions string) string {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "schemas", "manifest.v1.json"), schemaJSON)
	mustWrite(t, filepath.Join(root, "docs", "src", "content", "docs", "page.md"), page)
	if exemptions != "" {
		mustWrite(t, filepath.Join(root, "tools", "docs", "manifest-exemptions.tsv"), exemptions)
	}
	return root
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A miniature of the real schema: closed at every level, with one nested
// object and one array of objects, which is the shape that matters.
const schemaJSON = `{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "name": {"type": "string"},
    "services": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "properties": {
          "name": {"type": "string"},
          "port": {"type": "integer"},
          "health_path": {"type": "string"},
          "env": {"type": "array", "items": {"$ref": "#/$defs/envVar"}}
        }
      }
    },
    "github": {"$ref": "#/$defs/github"}
  },
  "$defs": {
    "github": {
      "type": "object",
      "additionalProperties": false,
      "properties": {"mode": {"type": "string"}, "teardown_on": {"type": "array"}}
    },
    "envVar": {
      "type": "object",
      "additionalProperties": false,
      "properties": {"name": {"type": "string"}, "value": {"type": "string"}}
    }
  }
}`

func check(t *testing.T, root string) (string, error) {
	t.Helper()
	out, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	runErr := run(root, out)
	body, _ := os.ReadFile(out.Name())
	return string(body), runErr
}

// The defect this gate was written for, and the one its first version could
// not see: a top level key the manifest does not have at any depth. The first
// version skipped exactly this block, because it used "no top level property"
// as the signal that a block was not a manifest.
func TestATopLevelKeyTheManifestDoesNotHaveIsReported(t *testing.T) {
	root := tree(t, "```yaml\ncontrol_plane:\n  url: https://cp.example.com\n```\n", "")
	if _, err := check(t, root); err == nil {
		t.Fatal("a key the manifest has at no depth was accepted")
	}
}

// A real key shown without its parent is how most of these pages are written,
// and reporting it would make the gate useless.
func TestAFragmentShowingARealKeyWithoutItsParentPasses(t *testing.T) {
	root := tree(t, "```yaml\nteardown_on: [closed, merged]\n```\n", "")
	if _, err := check(t, root); err != nil {
		t.Fatalf("a fragment of a real key was reported: %v", err)
	}
}

// The deep check: a whole manifest whose nested key is a typo.
func TestATypoInsideAWholeManifestIsReported(t *testing.T) {
	page := "```yaml\nname: x\nservices:\n  - name: web\n    healthpath: /typo\n```\n"
	root := tree(t, page, "")
	out, err := check(t, root)
	if err == nil {
		t.Fatalf("a typo nested in a manifest was accepted, output %q", out)
	}
}

// Arrays of objects are checked through their items schema, including one
// reached by a reference.
func TestAKeyInsideAnArrayOfReferencedObjectsIsChecked(t *testing.T) {
	page := "```yaml\nservices:\n  - name: web\n    env:\n      - name: PORT\n        vaule: \"3000\"\n```\n"
	root := tree(t, page, "")
	if _, err := check(t, root); err == nil {
		t.Fatal("a misspelled key inside a referenced array item was accepted")
	}
}

// Another product's configuration file does appear in these pages, and saying
// so in one reviewed line is better than the gate guessing.
func TestAnExemptedForeignBlockPasses(t *testing.T) {
	page := "```yaml\ndatabaseConfigs:\n  shared_buffers: 1GB\n```\n"
	ex := "docs/src/content/docs/page.md\tdatabaseConfigs\tanother product's file\n"
	root := tree(t, page, ex)
	if _, err := check(t, root); err != nil {
		t.Fatalf("an exempted foreign block was reported: %v", err)
	}
}

// An exemption nobody needs any more is reported, so the list cannot rot into
// a place findings go to be forgotten.
func TestAnExemptionThatIsNoLongerNeededIsReported(t *testing.T) {
	page := "```yaml\nname: x\n```\n"
	ex := "docs/src/content/docs/page.md\tgone\tno longer present\n"
	root := tree(t, page, ex)
	out, err := check(t, root)
	if err == nil {
		t.Fatalf("a stale exemption was accepted, output %q", out)
	}
}

// An exemption with no reason is refused, for the same reason the forbidden
// scan refuses one.
func TestAnExemptionWithoutAReasonIsRefused(t *testing.T) {
	root := tree(t, "```yaml\nname: x\n```\n", "docs/src/content/docs/page.md\tkey\t\n")
	if _, err := check(t, root); err == nil {
		t.Fatal("an exemption with an empty reason was accepted")
	}
}

// A block a reader is invited to copy has to parse, whatever it is.
func TestAYamlBlockThatDoesNotParseIsReported(t *testing.T) {
	root := tree(t, "```yaml\nname: [unclosed\n```\n", "")
	if _, err := check(t, root); err == nil {
		t.Fatal("an unparseable yaml block was accepted")
	}
}

// Blocks in another language are not this gate's business.
func TestBlocksThatAreNotYamlAreLeftAlone(t *testing.T) {
	root := tree(t, "```sh\ncontrol_plane: not yaml at all\n```\n", "")
	if _, err := check(t, root); err != nil {
		t.Fatalf("a shell block was checked as yaml: %v", err)
	}
}

// A run that checks nothing reports green, which is the failure mode every
// gate here is built to avoid.
func TestCheckingNothingIsAnError(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "schemas", "manifest.v1.json"), schemaJSON)
	_, err := check(t, root)
	if err == nil || !strings.Contains(err.Error(), "no pages") {
		t.Fatalf("err = %v, want a refusal to report green on nothing", err)
	}
}
