package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every licence this tool names is tested in both directions: recognised from
// its own text, copied from a module that carries it, and a text that only
// resembles it refused. A matcher proved only on the texts it accepts has never
// been shown able to say no, and saying no is the half a notices file depends on.

func fixture(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "licences", name))
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}
	return string(body)
}

func TestEachLicenceTheLinkedModulesCarryIsNamedFromItsOwnText(t *testing.T) {
	for _, c := range []struct{ file, want string }{
		{"Apache-2.0.txt", "Apache-2.0"},
		{"MIT.txt", "MIT"},
		{"BSD-3-Clause.txt", "BSD-3-Clause"},
		{"BSD-2-Clause.txt", "BSD-2-Clause"},
		{"ISC.txt", "ISC"},
		{"SQLite-public-domain.txt", "LicenseRef-SQLite-public-domain"},
	} {
		t.Run(c.want, func(t *testing.T) {
			// Exactly this and nothing else, so a three clause text also being
			// read as two clause fails here rather than passing a Contains.
			if got := strings.Join(identify(fixture(t, c.file)), " AND "); got != c.want {
				t.Errorf("identify(%s) = %q, want %q", c.file, got, c.want)
			}
		})
	}
}

func TestATextThatOnlyResemblesALicenceIsNamedAsNothing(t *testing.T) {
	for name, text := range map[string]string{
		"the Mozilla Public License header, a licence this tool does not know": "This Source Code Form is subject to the terms of the Mozilla Public License, v. 2.0.",
		"a bare copyright line": "Copyright 2020 Somebody. All rights reserved.",
		"the MIT grant without the condition that makes it the MIT licence": "Permission is hereby granted, free of charge, to any person obtaining a copy of this software.",
		"a sentence that names the Apache licence without carrying it":      "This project is licensed under the Apache License, Version 2.0.",
		"the four clause BSD licence, which must not be read as three clause": fixture(t, "BSD-3-Clause.txt") +
			"\n4. All advertising materials mentioning features or use of this software must display the following acknowledgement.\n",
	} {
		t.Run(name, func(t *testing.T) {
			if got := identify(text); len(got) != 0 {
				t.Errorf("identify named %v for a text that carries no licence this tool knows", got)
			}
		})
	}
}

func TestEveryLicenceInOneFileIsNamedAndNotOnlyTheFirst(t *testing.T) {
	apacheThenBSD := fixture(t, "Apache-2.0.txt") + "\n\n" + fixture(t, "BSD-3-Clause.txt")
	if got := strings.Join(identify(apacheThenBSD), " AND "); got != "Apache-2.0 AND BSD-3-Clause" {
		t.Errorf("an Apache licence with a BSD licence beneath it was named %q", got)
	}
	// A three clause and a two clause licence together: one endorsement clause
	// for two redistribution clauses, so both are named.
	both := fixture(t, "BSD-3-Clause.txt") + "\n\n" + fixture(t, "BSD-2-Clause.txt")
	if got := strings.Join(identify(both), " AND "); got != "BSD-2-Clause AND BSD-3-Clause" {
		t.Errorf("a three clause and a two clause licence together were named %q", got)
	}
}

func TestALicenceHeadingASourceFileIsStillRecognised(t *testing.T) {
	var commented strings.Builder
	for _, line := range strings.Split(fixture(t, "MIT.txt"), "\n") {
		commented.WriteString("// " + line + "\n")
	}
	if got := strings.Join(identify(commented.String()), " AND "); got != "MIT" {
		t.Errorf("an MIT licence in line comments was named %q", got)
	}
}

func moduleDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestAModuleWhoseLicenceIsRecognisedIsAttributed(t *testing.T) {
	dir := moduleDir(t, map[string]string{"LICENSE": fixture(t, "MIT.txt")})
	got, err := attribute([]module{{Path: "example.com/open", Version: "v1.0.0", Dir: dir}}, nil)
	if err != nil {
		t.Fatalf("a module carrying the MIT licence was refused: %v", err)
	}
	if got[0].Licence != "MIT" {
		t.Errorf("licence = %q, want MIT", got[0].Licence)
	}
}

func TestAModuleWhoseLicenceIsNotRecognisedFailsAndIsNamed(t *testing.T) {
	dir := moduleDir(t, map[string]string{"LICENSE": "Copyright 2020 Somebody. All rights reserved."})
	_, err := attribute([]module{{Path: "example.com/closed", Version: "v1.0.0", Dir: dir}}, nil)
	if err == nil {
		t.Fatal("a module whose licence was not recognised was attributed rather than refused")
	}
	for _, want := range []string{"example.com/closed", "LICENSE"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure does not name %q: %v", want, err)
		}
	}
}

func TestAModuleThatShipsNoLicenceFileFails(t *testing.T) {
	dir := moduleDir(t, map[string]string{"README.md": "no licence here"})
	_, err := attribute([]module{{Path: "example.com/bare", Version: "v1.0.0", Dir: dir}}, nil)
	if err == nil || !strings.Contains(err.Error(), "ships no licence file") {
		t.Fatalf("a module with no licence file was not refused by name: %v", err)
	}
}

func TestAModuleWithNoDirectoryInTheCacheFails(t *testing.T) {
	_, err := attribute([]module{{Path: "example.com/missing", Version: "v1.0.0"}}, nil)
	// The module cache wording as well as the name, because reading an empty
	// directory would also fail naming the module, and only this message tells
	// the reader the fix is a download rather than a licence decision.
	if err == nil || !strings.Contains(err.Error(), "example.com/missing") ||
		!strings.Contains(err.Error(), "no directory in the module cache") {
		t.Fatalf("a module with no directory was not refused by name and cause: %v", err)
	}
}

func TestEveryModuleThatFailsIsNamedAndNotOnlyTheFirst(t *testing.T) {
	a := moduleDir(t, map[string]string{"LICENSE": "All rights reserved."})
	b := moduleDir(t, map[string]string{"LICENSE": "Also all rights reserved."})
	_, err := attribute([]module{
		{Path: "example.com/first", Version: "v1.0.0", Dir: a},
		{Path: "example.com/second", Version: "v1.0.0", Dir: b},
	}, nil)
	if err == nil {
		t.Fatal("two unrecognised modules were attributed")
	}
	for _, want := range []string{"example.com/first", "example.com/second"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure stops before %s: %v", want, err)
		}
	}
}

func TestAnExtraFileNamedLikeALicenceIsRecognisedOrExplained(t *testing.T) {
	files := map[string]string{
		"LICENSE":      fixture(t, "MIT.txt"),
		"LICENSE-LOGO": "https://example.com/logo.png",
	}
	if _, err := attribute([]module{{Path: "example.com/logo", Version: "v1.0.0", Dir: moduleDir(t, files)}}, nil); err == nil ||
		!strings.Contains(err.Error(), "LICENSE-LOGO") {
		t.Fatalf("an unexplained file named like a licence passed: %v", err)
	}
	rules := map[string]fileRule{"example.com/logo LICENSE-LOGO": {reason: "a link to a logo"}}
	got, err := attribute([]module{{Path: "example.com/logo", Version: "v1.0.0", Dir: moduleDir(t, files)}}, rules)
	if err != nil {
		t.Fatalf("a file with a written rule was still refused: %v", err)
	}
	if got[0].Licence != "MIT" || len(got[0].Notices) != 0 {
		t.Errorf("licence %q, notices %d; the logo link must neither name a licence nor be reproduced",
			got[0].Licence, len(got[0].Notices))
	}
}

func TestARuleForAFileNoModuleShipsFails(t *testing.T) {
	dir := moduleDir(t, map[string]string{"LICENSE": fixture(t, "MIT.txt")})
	rules := map[string]fileRule{"example.com/gone LICENSE-GONE": {reason: "described a file that was removed"}}
	_, err := attribute([]module{{Path: "example.com/open", Version: "v1.0.0", Dir: dir}}, rules)
	if err == nil || !strings.Contains(err.Error(), "example.com/gone LICENSE-GONE") {
		t.Fatalf("a rule about a file nothing ships was accepted: %v", err)
	}
}

func TestNoticesAreCarriedVerbatimInsideAFenceTheyCannotClose(t *testing.T) {
	notice := "Copyright 2011 Somebody.\n```\nnot markdown\n```\n"
	third := "# Third-Party Software Notices\n\n" + fixture(t, "ISC.txt")
	dir := moduleDir(t, map[string]string{
		"LICENSE":              fixture(t, "Apache-2.0.txt"),
		"NOTICE":               notice,
		"LICENSE-3RD-PARTY.md": third,
	})
	rules := map[string]fileRule{"example.com/noted LICENSE-3RD-PARTY.md": {reproduce: true, reason: "notices"}}
	mods, err := attribute([]module{{Path: "example.com/noted", Version: "v1.0.0", Dir: dir}}, rules)
	if err != nil {
		t.Fatalf("attribute: %v", err)
	}
	if mods[0].Licence != "Apache-2.0 AND ISC" {
		t.Errorf("licence = %q; a reproduced document's own licences have to be named too", mods[0].Licence)
	}
	out := render([]target{{os: "linux", arch: "amd64"}}, mods)
	if !strings.Contains(out, "- `example.com/noted` v1.0.0, Apache-2.0 AND ISC") {
		t.Errorf("the module line does not name its licence:\n%s", out)
	}
	if !strings.Contains(out, "````text\n"+strings.TrimRight(notice, "\n")+"\n````") {
		t.Errorf("the NOTICE is not carried verbatim inside a fence longer than its own:\n%s", out)
	}
	if !strings.Contains(out, strings.TrimRight(third, "\n")) {
		t.Errorf("the reproduced notices document is missing:\n%s", out)
	}
}
