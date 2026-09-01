package main

import (
	"io"
	"strings"
	"testing"
)

// The tests run against the fixture below rather than against the real
// workflow, and that is deliberate. `go test` keys its cache on files opened
// under the test's own module, and .github/workflows/release.yml is not one, so
// a test reading it would go on reporting `ok (cached)` after somebody changed
// the thing it was checking. That exact trap already caught `tools/installsh`
// here. The real file is checked by the gate, `go run ./tools/releasecheck .`,
// which has no cache to be wrong about.
//
// Every case below is a mutation of one workflow that passes, so each one
// names the single thing it removed and nothing else is in question.
const good = `
name: Release
on:
  push:
    tags: ['v*']
permissions:
  contents: read
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@1111111111111111111111111111111111111111
      - name: Build and package
        run: ./tools/release/build.sh linux amd64 1.0.0 sha date dist stage
      - uses: actions/upload-artifact@2222222222222222222222222222222222222222
        with:
          path: dist/*.tar.gz*

  publish:
    needs: build
    runs-on: ubuntu-latest
    permissions:
      contents: write
      id-token: write
    steps:
      - uses: actions/download-artifact@3333333333333333333333333333333333333333
        with: { path: dist, merge-multiple: true }

      - name: Third party notices
        run: go run ./tools/notices > THIRD_PARTY_NOTICES.md

      - name: Checksums
        run: |
          cd dist
          cat *.sha256 > checksums.txt
          rm -f *.sha256

      - name: Software bill of materials
        uses: anchore/sbom-action@4444444444444444444444444444444444444444
        with:
          path: unpacked
          format: spdx-json
          output-file: dist/sbom.spdx.json
          upload-artifact: false

      - name: Install cosign
        uses: sigstore/cosign-installer@5555555555555555555555555555555555555555

      - name: Sign the checksums and the bill of materials
        run: |
          cosign sign-blob --yes \
            --bundle dist/checksums.txt.sigstore.json \
            dist/checksums.txt
          cosign sign-blob --yes \
            --bundle dist/sbom.spdx.json.sigstore.json \
            dist/sbom.spdx.json

      - name: The signature verifies
        run: |
          for blob in checksums.txt sbom.spdx.json; do
            cosign verify-blob --bundle "dist/$blob.sigstore.json" "dist/$blob"
          done

      - name: Release
        uses: softprops/action-gh-release@6666666666666666666666666666666666666666
        with:
          files: |
            dist/*.tar.gz
            dist/checksums.txt
            dist/checksums.txt.sigstore.json
            dist/sbom.spdx.json
            dist/sbom.spdx.json.sigstore.json
            THIRD_PARTY_NOTICES.md
          fail_on_unmatched_files: true
          generate_release_notes: true
`

// replace is one edit, and it asserts the text it is removing was there. A
// mutation test whose mutation silently did not apply is a test that passes
// over the unmutated original, which is the failure this whole command exists
// to talk about.
func replace(t *testing.T, source, old, new string) string {
	t.Helper()
	if strings.Count(source, old) != 1 {
		t.Fatalf("the fixture contains %d copies of %q and the mutation needs exactly one",
			strings.Count(source, old), old)
	}
	return strings.Replace(source, old, new, 1)
}

func problems(t *testing.T, source string) []string {
	t.Helper()
	found, err := check([]byte(source), io.Discard)
	if err != nil {
		t.Fatalf("checking the workflow: %v", err)
	}
	return found
}

func TestAWorkflowThatSignsAndPublishesWhatItSignedHasNoProblems(t *testing.T) {
	if found := problems(t, good); len(found) != 0 {
		t.Fatalf("the fixture is meant to pass, and it reported:\n  %s", strings.Join(found, "\n  "))
	}
}

// The verify step builds its bundle path from a shell variable. Reporting that
// as an unpublished signature would be a finding about this command rather
// than about the workflow, and one false finding is how a gate stops being
// read.
func TestABundlePathBuiltFromAShellVariableIsNotReportedAsUnpublished(t *testing.T) {
	for _, p := range problems(t, good) {
		if strings.Contains(p, "$blob") {
			t.Fatalf("a shell variable was read as a path: %s", p)
		}
	}
}

func TestEachDefectIsReported(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T) string
		want   string
	}{
		{
			// The one that would have let a short release publish. Removing
			// the input is exactly the state main was in before this landed.
			name: "the publishing step does not require its patterns to match",
			mutate: func(t *testing.T) string {
				return replace(t, good, "          fail_on_unmatched_files: true\n", "")
			},
			want: "does not set `fail_on_unmatched_files`",
		},
		{
			name: "the publishing step turns the requirement off",
			mutate: func(t *testing.T) string {
				return replace(t, good, "fail_on_unmatched_files: true", "fail_on_unmatched_files: false")
			},
			want: "`fail_on_unmatched_files: false`",
		},
		{
			name: "the signing job has no OIDC token",
			mutate: func(t *testing.T) string {
				return replace(t, good, "      id-token: write\n", "")
			},
			want: `"publish" job signs and its effective permissions are contents: write`,
		},
		{
			// A job-level block REPLACES the workflow-level one. A grant that
			// reads correctly at the top of the file is not a grant the job
			// has, and this is the case that says so.
			name: "the workflow grants the token and the job's own block drops it",
			mutate: func(t *testing.T) string {
				source := replace(t, good, "permissions:\n  contents: read\n",
					"permissions:\n  contents: read\n  id-token: write\n")
				return replace(t, source, "      id-token: write\n", "")
			},
			want: "cannot get one without `id-token: write`",
		},
		{
			name: "an asset is published that nothing before it names",
			mutate: func(t *testing.T) string {
				return replace(t, good, "            THIRD_PARTY_NOTICES.md\n",
					"            THIRD_PARTY_NOTICES.md\n            dist/provenance.json\n")
			},
			want: "`dist/provenance.json` is published and no step before it names that path",
		},
		{
			// The prefix trap. `dist/sbom.spdx.json` is a prefix of
			// `dist/sbom.spdx.json.sigstore.json`, so with the bill of
			// materials written and signed under another name, a plain
			// substring test would find the published path inside its own
			// bundle's name and pass over the release that had lost it.
			name: "the published path survives only as the head of its bundle's name",
			mutate: func(t *testing.T) string {
				source := replace(t, good, "output-file: dist/sbom.spdx.json", "output-file: dist/bom.json")
				return replace(t, source,
					"--bundle dist/sbom.spdx.json.sigstore.json \\\n            dist/sbom.spdx.json",
					"--bundle dist/sbom.spdx.json.sigstore.json \\\n            dist/bom.json")
			},
			want: "`dist/sbom.spdx.json` is published and no step before it names that path",
		},
		{
			name: "a signature is written and never published",
			mutate: func(t *testing.T) string {
				return replace(t, good,
					"            dist/checksums.txt.sigstore.json\n", "")
			},
			want: "`dist/checksums.txt.sigstore.json` is signed and is not in the published",
		},
		{
			name: "an archive glob points at a directory nothing writes into",
			mutate: func(t *testing.T) string {
				return replace(t, good, "            dist/*.tar.gz\n", "            release/*.tar.gz\n")
			},
			want: "no step before it names anything in release/",
		},
		{
			name: "a glob names no directory to ask about",
			mutate: func(t *testing.T) string {
				return replace(t, good, "            THIRD_PARTY_NOTICES.md\n", "            *.md\n")
			},
			want: "`*.md` is a glob with no directory",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			found := problems(t, tc.mutate(t))
			for _, p := range found {
				if strings.Contains(p, tc.want) {
					return
				}
			}
			t.Fatalf("nothing reported %q. What was reported:\n  %s",
				tc.want, strings.Join(found, "\n  "))
		})
	}
}

// The two shapes that mean this command is reading something it cannot reason
// about. Both are errors rather than problems, because not knowing and knowing
// something is wrong want different messages to whoever has to fix it.
func TestItRefusesAWorkflowItCannotReasonAbout(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "nothing publishes a release",
			source: replace(t, good, "        uses: softprops/action-gh-release@6666666666666666666666666666666666666666\n", "        uses: actions/upload-artifact@2222222222222222222222222222222222222222\n"),
			want:   "nothing here publishes a release",
		},
		{
			name:   "the workflow has no jobs at all",
			source: "name: Release\non:\n  push:\n    tags: ['v*']\n",
			want:   "declares no jobs",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := check([]byte(tc.source), io.Discard)
			if err == nil {
				t.Fatalf("expected an error mentioning %q and got none", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected an error mentioning %q, got: %v", tc.want, err)
			}
		})
	}
}

// `write-all` is the other shape the permissions key takes, and reading it as a
// mapping would report a job that holds every token as holding none.
func TestTheWriteAllShorthandGrantsTheSigningToken(t *testing.T) {
	source := replace(t, good, "    permissions:\n      contents: write\n      id-token: write\n",
		"    permissions: write-all\n")
	for _, p := range problems(t, source) {
		if strings.Contains(p, "id-token") {
			t.Fatalf("write-all was read as granting no token: %s", p)
		}
	}
}
