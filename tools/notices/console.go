package main

// THE CONSOLE IS ATTRIBUTED FROM WHAT ITS EXPORT CONTAINS, NOT FROM ITS
// LOCKFILE, and the difference is not small. The console's lockfile lists
// fifty five production packages, eleven of them LGPL 3.0, most of them the
// native image library behind Next's image optimisation. The image does not
// ship the console's node_modules at all. It ships a static export, and a build
// measured on 2026-09-12 bundled next, @swc/helpers, the React packages Next
// vendors inside itself, and two font files. A notice written from the
// lockfile would have told a buyer the image contains LGPL code it does not
// contain.
//
// So the console is built the way the image builds it, with browser source maps
// switched on in the temporary copy only, and every source path in those maps
// that runs through node_modules names a package the export carries. Static
// assets have no source map, so each one is traced by its bytes: a file
// identical to one inside an installed package is that package's, a file
// identical to one the console tracks is the console's own, and a file matching
// neither is a failure. Nothing is attributed by name alone.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// sourcePackage finds the package a bundled source came from. The second group
// is a package Next vendors inside itself under dist/compiled, which ships as
// part of next and carries its own licence file.
var sourcePackage = regexp.MustCompile(`node_modules/((?:@[^/]+/)?[^/]+)(?:/dist/compiled/((?:@[^/]+/)?[^/]+))?`)

// consoleBundle builds the console as the image's console stage does and
// returns the packages its static export contains.
func consoleBundle(root, npm, tmp string) ([]npmPackage, error) {
	st, err := parseStage(filepath.Join(root, "deploy", "docker", "control-plane.Dockerfile"), "console")
	if err != nil {
		return nil, err
	}
	dir, err := replay(root, st, tmp, npm, gitTracked)
	if err != nil {
		return nil, err
	}
	if err := withSourceMaps(dir); err != nil {
		return nil, err
	}
	cmd := exec.Command(npm, "run", "build")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "NEXT_TELEMETRY_DISABLED=1", "npm_config_update_notifier=false")
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("building the console to read what its export contains failed: %w\n%s",
			err, tail(string(out), 12))
	}
	own, err := gitTracked(filepath.Join(root, "console"))
	if err != nil {
		return nil, err
	}
	var ownPaths []string
	for _, f := range own {
		ownPaths = append(ownPaths, filepath.Join(root, "console", f))
	}
	return exported(dir, ownPaths)
}

// withSourceMaps turns on browser source maps in the temporary copy's Next
// config. It refuses a config it cannot find the export line in, because a
// build without maps would read as an export that bundles nothing.
func withSourceMaps(dir string) error {
	matches, err := filepath.Glob(filepath.Join(dir, "next.config.*"))
	if err != nil || len(matches) != 1 {
		return fmt.Errorf("expected one next.config in %s, found %v", dir, matches)
	}
	raw, err := os.ReadFile(matches[0])
	if err != nil {
		return err
	}
	const export = `output: "export"`
	if n := strings.Count(string(raw), export); n != 1 {
		return fmt.Errorf("%s holds %q %d times, so source maps cannot be switched on beside it, and "+
			"without them what the export contains cannot be read", matches[0], export, n)
	}
	patched := strings.Replace(string(raw), export, export+", productionBrowserSourceMaps: true", 1)
	return os.WriteFile(matches[0], []byte(patched), 0o644)
}

// exported reads a built console: every package its source maps name, and the
// package or console file every static asset is byte for byte identical to.
func exported(dir string, own []string) ([]npmPackage, error) {
	out := filepath.Join(dir, "out")
	pkgDirs := map[string]bool{}
	var assets []string
	maps := 0
	err := filepath.WalkDir(out, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		switch ext := filepath.Ext(path); ext {
		case ".map":
			maps++
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			var m struct {
				Sources []string `json:"sources"`
			}
			if err := json.Unmarshal(raw, &m); err != nil {
				return fmt.Errorf("reading the source map %s: %w", path, err)
			}
			for _, s := range m.Sources {
				if match := sourcePackage.FindStringSubmatch(s); match != nil {
					p := filepath.Join(dir, "node_modules", filepath.FromSlash(match[1]))
					if match[2] != "" {
						p = filepath.Join(p, "dist", "compiled", filepath.FromSlash(match[2]))
					}
					pkgDirs[p] = true
				}
			}
		case ".js", ".css", ".html", ".txt", ".json":
		default:
			if strings.Contains(filepath.ToSlash(path), "/_next/static/") {
				assets = append(assets, path)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading the export in %s: %w", out, err)
	}
	if maps == 0 {
		return nil, fmt.Errorf("the console build in %s wrote no source maps, so what its export contains "+
			"was not read", dir)
	}
	if len(pkgDirs) == 0 {
		return nil, fmt.Errorf("%d source maps in %s name no package from node_modules, which a Next "+
			"export always contains; the maps were not read the way this expects", maps, out)
	}

	var problems []string
	found := map[string]npmPackage{}
	for p := range pkgDirs {
		pkg, errs := attributeBundled(dir, p)
		problems = append(problems, errs...)
		if len(errs) == 0 && pkg.Name != "" {
			found[pkg.Name+"@"+pkg.Version] = pkg
		}
	}

	traced, errs := traceAssets(dir, assets, own)
	problems = append(problems, errs...)
	for p := range traced {
		pkg, errs := attributeBundled(dir, p)
		problems = append(problems, errs...)
		if len(errs) == 0 && pkg.Name != "" {
			found[pkg.Name+"@"+pkg.Version] = pkg
		}
	}

	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, fmt.Errorf("%d part(s) of the console export could not be attributed:\n  %s",
			len(problems), strings.Join(problems, "\n  "))
	}
	pkgs := make([]npmPackage, 0, len(found))
	for _, p := range found {
		pkgs = append(pkgs, p)
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].Name < pkgs[j].Name })
	return pkgs, nil
}

// attributeBundled attributes one package directory the export draws on.
//
// A package Next vendors under dist/compiled has a manifest with no version and
// no licence field, so it is named by where it sits, versioned by the next that
// carries it, and attributed from its licence text alone. There is no
// declaration to fall back on, and a vendored copy with no recognisable licence
// file is a failure.
func attributeBundled(dir, pkgDir string) (npmPackage, []string) {
	compiled := filepath.Join("dist", "compiled") + string(filepath.Separator)
	if !strings.Contains(pkgDir, compiled) {
		return attributePackage(pkgDir)
	}
	rel, err := filepath.Rel(filepath.Join(dir, "node_modules"), pkgDir)
	if err != nil {
		return npmPackage{}, []string{err.Error()}
	}
	host := strings.SplitN(filepath.ToSlash(rel), "/dist/compiled/", 2)[0]
	hostPkg, errs := attributePackage(filepath.Join(dir, "node_modules", filepath.FromSlash(host)))
	if len(errs) > 0 {
		return npmPackage{}, errs
	}
	pkg := npmPackage{Name: filepath.ToSlash(rel), Version: "vendored in " + hostPkg.Name + " " + hostPkg.Version}
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		return pkg, []string{fmt.Sprintf("%s: %v", pkg.Name, err)}
	}
	ids := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !licenceFile.MatchString(e.Name()) {
			continue
		}
		body, err := os.ReadFile(filepath.Join(pkgDir, e.Name()))
		if err != nil {
			return pkg, []string{fmt.Sprintf("%s: reading %s: %v", pkg.Name, e.Name(), err)}
		}
		for _, id := range identify(string(body)) {
			ids[id] = true
		}
	}
	if len(ids) == 0 {
		return pkg, []string{fmt.Sprintf("%s is bundled into the console export and carries no licence "+
			"file this tool recognises; a vendored copy has no declaration to fall back on", pkg.Name)}
	}
	names := make([]string, 0, len(ids))
	for id := range ids {
		names = append(names, id)
	}
	sort.Strings(names)
	pkg.Licence = strings.Join(names, " AND ")
	return pkg, nil
}

// traceAssets matches every static asset by content. It returns the package
// directories the assets came from; an asset identical to a file the console
// tracks is the console's own and needs no attribution, and an asset matching
// nothing is a problem.
func traceAssets(dir string, assets, own []string) (map[string]bool, []string) {
	pkgs := map[string]bool{}
	if len(assets) == 0 {
		return pkgs, nil
	}
	wanted := map[string][]string{}
	for _, a := range assets {
		h, err := hashFile(a)
		if err != nil {
			return pkgs, []string{err.Error()}
		}
		wanted[h] = append(wanted[h], a)
	}
	matched := map[string]bool{}
	for _, f := range own {
		if h, err := hashFile(f); err == nil && wanted[h] != nil {
			matched[h] = true
		}
	}
	modules := filepath.Join(dir, "node_modules")
	walkErr := filepath.WalkDir(modules, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > 64<<20 {
			return nil
		}
		h, err := hashFile(path)
		if err != nil || wanted[h] == nil {
			return nil
		}
		matched[h] = true
		if p := packageRoot(modules, path); p != "" {
			pkgs[p] = true
		}
		return nil
	})
	var problems []string
	if walkErr != nil && !errors.Is(walkErr, fs.ErrNotExist) {
		problems = append(problems, fmt.Sprintf("walking %s: %v", modules, walkErr))
	}
	for h, files := range wanted {
		if !matched[h] {
			for _, f := range files {
				problems = append(problems, fmt.Sprintf("%s in the console export is identical to no file in an "+
					"installed package and none the console tracks, so where it came from is not known", filepath.Base(f)))
			}
		}
	}
	return pkgs, problems
}

// packageRoot returns the package directory a file under node_modules belongs
// to, the innermost one when packages are nested.
func packageRoot(modules, path string) string {
	rel, err := filepath.Rel(modules, path)
	if err != nil {
		return ""
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	root := ""
	for i := 0; i < len(parts)-1; i++ {
		if i == 0 || parts[i-1] == "node_modules" {
			n := 1
			if strings.HasPrefix(parts[i], "@") && i+1 < len(parts)-1 {
				n = 2
			}
			root = filepath.Join(modules, filepath.FromSlash(strings.Join(parts[:i+n], "/")))
		}
	}
	return root
}

func hashFile(path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}
