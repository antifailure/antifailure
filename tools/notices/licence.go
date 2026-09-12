package main

// THE FAILURE THIS FILE IS FOR. The Go module list named each module and its
// version and nothing else. A procurement reviewer reads this file to learn
// what the binary is licensed under, and ninety eight entries with no licence
// named answered none of it. The Apache License 2.0, which covers about half of
// them, also asks in section 4(d) that a NOTICE file shipped with a work be
// reproduced with it, and none was.
//
// So each module's licence is read out of the module cache, from the files the
// module itself ships, and named as an SPDX expression. It is recognised rather
// than guessed: a small matcher written for the licence texts the linked modules
// actually carry, measured before it was written, and anything it does not
// recognise is a failure naming the module and the file. A notices generator
// that skipped what it could not read would publish a legal document with a hole
// in it and stay green, which is the one outcome worse than no generator.
//
// Why not github.com/google/licensecheck. It is the classifier this idea is
// usually reached for, its last release is from 2020, and it answers a wider
// question with a confidence score. The question here is narrow and the answer
// has to be yes or a named failure, so a matcher that knows six texts exactly
// and refuses the seventh is the smaller and the more honest instrument.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// licenceText is one licence the matcher names. Every phrase has to appear in
// the normalised text, which is what stops a file that merely mentions a
// licence, or quotes half of one, from being read as carrying it.
type licenceText struct {
	id      string
	phrases []string
}

var licences = []licenceText{
	{"Apache-2.0", []string{
		"apache license version 2.0, january 2004",
		"terms and conditions for use, reproduction, and distribution",
	}},
	{"MIT", []string{
		"permission is hereby granted, free of charge, to any person obtaining a copy",
		"the above copyright notice and this permission notice shall be included in all copies or substantial portions of the software",
	}},
	{"ISC", []string{
		"permission to use, copy, modify, and/or distribute this software for any purpose with or without fee is hereby granted, provided that the above copyright notice and this permission notice appear in all copies",
	}},
	// SQLite is not under a licence at all. Its authors dedicated it to the
	// public domain, and modernc.org/sqlite ships that dedication beside its own
	// licence because the code it carries is SQLite's. SPDX has no identifier for
	// the dedication, so it is named as a LicenseRef rather than dressed up as
	// one of the identifiers it is not.
	{"LicenseRef-SQLite-public-domain", []string{
		"sqlite is public domain",
		"dedicated to the public domain",
	}},
}

// The BSD licences are recognised by counting rather than by phrase, because
// the two clause text is contained in the three clause one. A three clause
// licence has one endorsement clause for each redistribution clause; any
// redistribution clause left over is a two clause licence beside it.
const bsdRedistribution = "redistribution and use in source and binary forms, with or without modification, are permitted provided that the following conditions are met"

var bsdEndorsement = regexp.MustCompile(
	`(?:neither the names? of|the names? of).{0,300}?(?:may|shall) not be used to endorse` +
		`|neither the names? of.{0,300}?may be used to endorse`)

// bsdAdvertising is the clause that makes a BSD licence the four clause one.
// That is not a licence this tool names, and reading it as three clause would
// drop exactly the obligation that distinguishes it, so a text carrying it is
// recognised as nothing and the run fails for a person to decide.
const bsdAdvertising = "all advertising materials mentioning features or use of this software"

var commentMarker = regexp.MustCompile(`(?m)^[ \t]*(?://|/\*|\*/|\*|#)[ \t]?`)

// normalise removes what a licence text picks up from where it is kept: comment
// markers when it heads a source file, typographic quotes, line wrapping and
// case. What is left is compared phrase by phrase.
func normalise(text string) string {
	text = commentMarker.ReplaceAllString(text, " ")
	text = strings.NewReplacer("“", `"`, "”", `"`, "‘", "'", "’", "'").Replace(text)
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}

// identify names every licence the text carries, sorted, and nothing when it
// recognises none.
//
// Every one rather than the first. Five OpenTelemetry modules and
// sigs.k8s.io/json carry the Apache License with a BSD licence beneath it in the
// same file, and sigs.k8s.io/yaml carries three, so a matcher that stopped at
// its first match would under attribute seven modules and look finished.
func identify(text string) []string {
	t := normalise(text)
	if strings.Contains(t, bsdAdvertising) {
		return nil
	}
	found := map[string]bool{}
	for _, l := range licences {
		all := true
		for _, p := range l.phrases {
			if !strings.Contains(t, p) {
				all = false
				break
			}
		}
		if all {
			found[l.id] = true
		}
	}
	redistribution := strings.Count(t, bsdRedistribution)
	endorsement := len(bsdEndorsement.FindAllStringIndex(t, -1))
	if endorsement > 0 {
		found["BSD-3-Clause"] = true
	}
	if redistribution > endorsement {
		found["BSD-2-Clause"] = true
	}
	ids := make([]string, 0, len(found))
	for id := range found {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

var (
	// licenceFile is every file a module ships that is named like a licence,
	// LICENSE-SQLITE and LICENSE.docs included. All of them are read, because
	// the extra ones are where a second licence lives.
	licenceFile = regexp.MustCompile(`(?i)^(?:licen[cs]e|copying|unlicense)(?:[-._].*)?$`)
	noticeFile  = regexp.MustCompile(`(?i)^notice(?:\.(?:txt|md))?$`)
)

// fileRule says what a file named like a licence is, when it is not a licence
// this tool names. reproduce carries the file into the notices verbatim.
type fileRule struct {
	reproduce bool
	reason    string
}

// fileRules are keyed by module path and file name, and every one is a decision
// written down with its reason. A rule that matches no file a linked module
// ships fails the run, for the reason claimcheck's exclusions and
// .govulncheck.yaml's entries do: a rule about a file that is not there
// describes nothing, and it would silently cover whatever takes that name next.
var fileRules = map[string]fileRule{
	"modernc.org/memory LICENSE-LOGO": {
		reason: "a single link to the project's logo image, which is not a licence and is not part of the binary",
	},
	"github.com/opencontainers/go-digest LICENSE.docs": {
		reason: "the Creative Commons licence for the module's documentation, which is not linked into the binary, so naming it would attribute something the binary does not contain",
	},
	"modernc.org/libc LICENSE-3RD-PARTY.md": {
		reproduce: true,
		reason:    "the module's own notices for code it carries from other projects, reproduced whole because it names terms beyond the module's own licence",
	},
}

// shipped is a notice a module distributes, carried into the file as it is.
type shipped struct {
	file string
	text string
}

// attribute fills in each module's licence and the notices it ships.
//
// It returns every module it could not attribute rather than the first, so one
// run says the whole of what needs a decision instead of one failure per run.
func attribute(mods []module, rules map[string]fileRule) ([]module, error) {
	used := map[string]bool{}
	var problems []string
	out := make([]module, len(mods))
	for i, m := range mods {
		got, errs := attributeOne(m, rules, used)
		out[i] = got
		problems = append(problems, errs...)
	}
	for key := range rules {
		if !used[key] {
			problems = append(problems, fmt.Sprintf("the rule for %s describes a file no linked module "+
				"ships; delete it, because a rule about nothing covers whatever takes the name next", key))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, fmt.Errorf("%d linked module file(s) could not be attributed, and a notices file "+
			"that skipped them would name licences for some of the binary and not the rest:\n  %s",
			len(problems), strings.Join(problems, "\n  "))
	}
	return out, nil
}

func attributeOne(m module, rules map[string]fileRule, used map[string]bool) (module, []string) {
	if m.Dir == "" {
		return m, []string{fmt.Sprintf("%s %s has no directory in the module cache, so its licence "+
			"cannot be read; run go mod download", m.Path, m.Version)}
	}
	entries, err := os.ReadDir(m.Dir)
	if err != nil {
		return m, []string{fmt.Sprintf("%s %s: reading %s: %v", m.Path, m.Version, m.Dir, err)}
	}

	ids := map[string]bool{}
	var problems []string
	sawLicence := false
	for _, e := range entries {
		name := e.Name()
		isLicence, isNotice := licenceFile.MatchString(name), noticeFile.MatchString(name)
		if e.IsDir() || (!isLicence && !isNotice) {
			continue
		}
		body, err := os.ReadFile(filepath.Join(m.Dir, name))
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: reading %s: %v", m.Path, name, err))
			continue
		}
		if isNotice {
			m.Notices = append(m.Notices, shipped{file: name, text: string(body)})
			continue
		}

		sawLicence = true
		key := m.Path + " " + name
		rule, ruled := rules[key]
		if ruled {
			used[key] = true
			if !rule.reproduce {
				continue
			}
			m.Notices = append(m.Notices, shipped{file: name, text: string(body)})
		}
		found := identify(string(body))
		if len(found) == 0 {
			if ruled {
				// A reproduced document need not name a licence itself; it is
				// carried whole, which is the obligation it creates.
				continue
			}
			problems = append(problems, fmt.Sprintf("%s ships %s, which is no licence this tool "+
				"recognises; teach the matcher the licence if it is one, or add a rule saying what "+
				"the file is", m.Path, name))
			continue
		}
		for _, id := range found {
			ids[id] = true
		}
	}

	switch {
	case !sawLicence:
		problems = append(problems, fmt.Sprintf("%s %s ships no licence file at all", m.Path, m.Version))
	case len(ids) == 0 && len(problems) == 0:
		problems = append(problems, fmt.Sprintf("%s %s ships licence files and none of them names a "+
			"licence this tool recognises", m.Path, m.Version))
	}

	names := make([]string, 0, len(ids))
	for id := range ids {
		names = append(names, id)
	}
	sort.Strings(names)
	m.Licence = strings.Join(names, " AND ")
	return m, problems
}

// fenceFor returns a code fence longer than any run of backticks in the text,
// so a notice that contains a fence of its own cannot close the block it is
// quoted in and spill into the file as markdown.
func fenceFor(text string) string {
	longest, run := 0, 0
	for _, r := range text {
		if r == '`' {
			run++
			if run > longest {
				longest = run
			}
			continue
		}
		run = 0
	}
	n := 3
	if longest >= n {
		n = longest + 1
	}
	return strings.Repeat("`", n)
}
