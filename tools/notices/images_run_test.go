package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These run images against the repository's real Dockerfiles and console, with
// a stand-in for npm, so what they prove is the wiring: that each section is
// read from the stage the image really installs in, that the enterprise
// additions are what its second install adds, and that no temporary directory
// outlives the run whether it succeeds or fails.

// imagesNPM is a shell script standing in for npm. `npm ci` installs one package
// named after a checksum of the package.json it was run beside, so two stages
// installing the same manifest install the same package and a different manifest
// installs a different one. `npm run build` writes a source map naming it, or
// fails when failBuild is set.
func imagesNPM(t *testing.T, failBuild bool) string {
	t.Helper()
	mit, err := filepath.Abs(filepath.Join("testdata", "licences", "MIT.txt"))
	if err != nil {
		t.Fatal(err)
	}
	fail := "0"
	if failBuild {
		fail = "1"
	}
	script := `#!/bin/sh
set -e
dep="dep-$(cksum package.json | cut -d' ' -f1)"
case "$1" in
ci)
  mkdir -p "node_modules/$dep"
  printf '{"name":"%s","version":"1.0.0","license":"MIT"}' "$dep" > "node_modules/$dep/package.json"
  cp '` + mit + `' "node_modules/$dep/LICENSE"
  ;;
run)
  if [ ` + fail + ` = 1 ]; then echo 'console build failed on purpose'; exit 1; fi
  mkdir -p out/_next/static/chunks
  printf 'x' > out/_next/static/chunks/a.js
  printf '{"version":3,"sources":["turbopack:///[project]/node_modules/%s/index.js"]}' "$dep" > out/_next/static/chunks/a.js.map
  ;;
*)
  echo "unexpected npm $*"; exit 2
  ;;
esac
`
	bin := filepath.Join(t.TempDir(), "npm")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// isolatedTemp points the process's temporary directory at an empty one and
// returns it. Every t.TempDir the test needs must be made before this, or it
// would land inside the directory the test expects to find empty.
func isolatedTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	return dir
}

func leftBehind(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestTheImageSectionsAreReadFromTheStagesTheImagesInstallIn(t *testing.T) {
	npm := imagesNPM(t, false)
	tmp := isolatedTemp(t)
	in, err := images(filepath.Join("..", ".."), npm)
	if err != nil {
		t.Fatalf("images: %v", err)
	}
	if len(in.community) != 1 || len(in.console) != 1 || len(in.enterprise) != 1 {
		t.Fatalf("community %+v, console %+v, enterprise %+v: want one package in each", in.community, in.console, in.enterprise)
	}
	// The enterprise image's first install is the same web manifest the community
	// image installs, so it adds nothing; its second, ee/web, is the addition.
	if in.enterprise[0].Name == in.community[0].Name {
		t.Errorf("the enterprise additions repeat the community install %s", in.community[0].Name)
	}
	if in.console[0].Name == in.community[0].Name || in.console[0].Licence != "MIT" {
		t.Errorf("the console section is %+v, which is not what its own install and build contain", in.console[0])
	}
	if left := leftBehind(t, tmp); len(left) > 0 {
		t.Errorf("a successful run left temporary directories behind: %v", left)
	}
}

func TestAFailedRunLeavesNoTemporaryDirectoryBehind(t *testing.T) {
	npm := imagesNPM(t, true)
	tmp := isolatedTemp(t)
	_, err := images(filepath.Join("..", ".."), npm)
	if err == nil || !strings.Contains(err.Error(), "console build failed on purpose") {
		t.Fatalf("a failing console build was not reported as one: %v", err)
	}
	if left := leftBehind(t, tmp); len(left) > 0 {
		t.Errorf("a failed run left temporary directories behind: %v", left)
	}
}
