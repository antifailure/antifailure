package main

// An npm package is attributed from what it ships, the same rule the Go modules
// follow, and the reason is a measured one rather than tidiness. posthog-node and
// @posthog/types declare MIT in package.json, and the LICENSE file each one
// ships carries the Apache License 2.0 as well as MIT. A notice written from the
// declared field would have under attributed both, in the file whose only job is
// to attribute.
//
// So a licence file, when a package ships one, is read with the same matcher as
// a Go module's and its answer is the licence. A package that ships no licence
// file at all leaves the declaration as the only statement there is, and four
// packages the control plane links are like that today. Their declaration is
// accepted only when it names licences a reader can look up by identifier, and
// the notice says it was declared with no licence file shipped, so nobody
// mistakes it for a text that was read.

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// npmPackage is one third party package an image carries.
type npmPackage struct {
	Name     string
	Version  string
	Licence  string
	Declared bool // the licence came from package.json because no licence file shipped
	Notices  []shipped
}

// knownDeclared are the SPDX identifiers accepted from a package.json alone. Each
// is a licence whose terms are published under that identifier, which is what
// makes a bare identifier an attribution rather than a guess. A declaration
// outside this set, such as "SEE LICENSE IN LICENSE.md" with no such file, is a
// failure naming the package.
var knownDeclared = map[string]bool{
	"0BSD": true, "Apache-2.0": true, "BSD-2-Clause": true, "BSD-3-Clause": true,
	"ISC": true, "MIT": true, "Unlicense": true,
}

// ownPackage is this repository's own code, which a third party notice does not
// attribute. npm installs a workspace as a link, and links are skipped anyway;
// the name rule is what still holds if an install ever copies them instead.
var ownPackage = regexp.MustCompile(`^@antifailure/`)

var spdxOperator = regexp.MustCompile(`\s+(?:AND|OR|WITH)\s+`)

// installed attributes every third party package under dir/node_modules, once
// per name and version, and returns every package it could not attribute rather
// than the first.
func installed(dir string) ([]npmPackage, error) {
	base := filepath.Join(dir, "node_modules")
	if _, err := os.Stat(base); err != nil {
		return nil, fmt.Errorf("%s has no node_modules, so the install attributed nothing: %w", dir, err)
	}
	seen := map[string]npmPackage{}
	var problems []string
	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// WalkDir does not follow links, and reports one as an entry that is not a
		// directory, so a workspace package npm installed as a link, which is this
		// repository's own code, is passed over here with the plain files.
		if !d.IsDir() || !isPackageDir(path) {
			return nil
		}
		pkg, errs := attributePackage(path)
		problems = append(problems, errs...)
		if pkg.Name == "" || ownPackage.MatchString(pkg.Name) {
			return nil
		}
		key := pkg.Name + "@" + pkg.Version
		if _, ok := seen[key]; !ok && len(errs) == 0 {
			seen[key] = pkg
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking %s: %w", base, err)
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, fmt.Errorf("%d installed package(s) could not be attributed, and a notice that "+
			"skipped them would name licences for some of the image and not the rest:\n  %s",
			len(problems), strings.Join(problems, "\n  "))
	}
	out := make([]npmPackage, 0, len(seen))
	for _, p := range seen {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Version < out[j].Version
	})
	return out, nil
}

// isPackageDir reports whether a directory is a package root inside
// node_modules: a child of node_modules, or of an @scope directory inside one.
func isPackageDir(path string) bool {
	parent := filepath.Base(filepath.Dir(path))
	name := filepath.Base(path)
	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "@") {
		return false
	}
	if parent == "node_modules" {
		return true
	}
	return strings.HasPrefix(parent, "@") && filepath.Base(filepath.Dir(filepath.Dir(path))) == "node_modules"
}

func attributePackage(dir string) (npmPackage, []string) {
	raw, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		// A directory under node_modules with no manifest is not a package, for
		// example a package's own nested folder that happens to sit there.
		return npmPackage{}, nil
	}
	var meta struct {
		Name     string          `json:"name"`
		Version  string          `json:"version"`
		License  json.RawMessage `json:"license"`
		Licenses []struct {
			Type string `json:"type"`
		} `json:"licenses"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return npmPackage{}, []string{fmt.Sprintf("%s: reading package.json: %v", dir, err)}
	}
	pkg := npmPackage{Name: meta.Name, Version: meta.Version}
	if pkg.Name == "" || ownPackage.MatchString(pkg.Name) {
		return pkg, nil
	}
	declared := declaredLicence(meta.License, meta.Licenses)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return pkg, []string{fmt.Sprintf("%s@%s: %v", pkg.Name, pkg.Version, err)}
	}
	ids := map[string]bool{}
	var licenceFiles []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		isLicence, isNotice := licenceFile.MatchString(name), noticeFile.MatchString(name)
		if !isLicence && !isNotice {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return pkg, []string{fmt.Sprintf("%s@%s: reading %s: %v", pkg.Name, pkg.Version, name, err)}
		}
		if isNotice {
			pkg.Notices = append(pkg.Notices, shipped{file: name, text: string(body)})
			continue
		}
		licenceFiles = append(licenceFiles, name)
		for _, id := range identify(string(body)) {
			ids[id] = true
		}
	}

	switch {
	case len(ids) > 0:
		names := make([]string, 0, len(ids))
		for id := range ids {
			names = append(names, id)
		}
		sort.Strings(names)
		pkg.Licence = strings.Join(names, " AND ")
	case len(licenceFiles) > 0:
		// The package ships a licence text and it is not one this tool knows.
		// The declaration is not trusted in its place: the text is what binds.
		return pkg, []string{fmt.Sprintf("%s@%s ships %s, which is no licence this tool recognises "+
			"(package.json declares %q); teach the matcher the licence rather than trusting the "+
			"declaration over the text", pkg.Name, pkg.Version, strings.Join(licenceFiles, ", "), declared)}
	case knownExpression(declared):
		pkg.Licence = declared
		pkg.Declared = true
	default:
		return pkg, []string{fmt.Sprintf("%s@%s ships no licence file and declares %q, which is not a "+
			"licence identifier this accepts on a declaration alone", pkg.Name, pkg.Version, declared)}
	}
	return pkg, nil
}

// declaredLicence reads the licence a package.json declares, in either the
// current string form, the older object form, or the deprecated array.
func declaredLicence(license json.RawMessage, legacy []struct {
	Type string `json:"type"`
}) string {
	var s string
	if len(license) > 0 && json.Unmarshal(license, &s) == nil {
		return strings.TrimSpace(s)
	}
	var obj struct {
		Type string `json:"type"`
	}
	if len(license) > 0 && json.Unmarshal(license, &obj) == nil && obj.Type != "" {
		return obj.Type
	}
	var types []string
	for _, l := range legacy {
		types = append(types, l.Type)
	}
	return strings.Join(types, " OR ")
}

// knownExpression accepts an SPDX identifier from knownDeclared, or an
// expression made only of them, parenthesised or not.
func knownExpression(expr string) bool {
	expr = strings.TrimSpace(strings.Trim(strings.TrimSpace(expr), "()"))
	if expr == "" {
		return false
	}
	for _, id := range spdxOperator.Split(expr, -1) {
		if !knownDeclared[strings.Trim(id, "() ")] {
			return false
		}
	}
	return true
}
