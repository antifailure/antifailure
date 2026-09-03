// Command lintgen turns engine/internal/insights/lintcatalog.yaml into Go
// code, a documentation page, a published catalogue, and a register of every
// identifier ever assigned.
//
// The lint is meant to move. Rules are added and sharpened, a name gets
// clearer, a finding's wording is rewritten the day somebody misreads it. None
// of that can break a consumer, and it does break one the moment the only
// thing a consumer can match on is the name. So a finding carries an
// identifier that is assigned once and never changes, and everything else
// about it stays free.
//
// The register is what makes that a promise rather than an intention. It
// records every identifier this repository has ever handed out, and this
// command refuses to run if one of them has gone missing from the catalogue.
// Appending is the only edit it will make: an identifier cannot be reassigned
// to a different rule by regenerating, only by deleting a line somebody has to
// delete on purpose and a reviewer can see.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// idPattern is the shape of an identifier.
//
// Deliberately not the shape of an error code. AF-DB-030 is a refusal with a
// message and a next step and a page of its own; LINT-004 is a thing the lint
// noticed about a migration that ran fine. Somebody who searches the error
// reference for a lint identifier and finds nothing has been told the truth,
// which is better than finding a page about something else.
var idPattern = regexp.MustCompile(`^LINT-\d{3}$`)

// rulePattern is the shape of a rule name: what the finding carries in JSON.
var rulePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

type entry struct {
	ID    string `yaml:"id"`
	Rule  string `yaml:"rule"`
	Title string `yaml:"title"`
	// Retired says why a rule is gone. The entry stays, so the identifier
	// stays spoken for: a filter written against it never silently starts
	// matching a different finding.
	Retired string `yaml:"retired"`
}

type catalog struct {
	Findings []entry `yaml:"findings"`
}

// register is the append only record of what has been handed out.
type register struct {
	Note     string          `json:"note"`
	Assigned []registerEntry `json:"assigned"`
}

type registerEntry struct {
	ID string `json:"id"`
	// Rule is the name the identifier was first assigned to. Historical: the
	// name is free to change and this is not compared against anything. It is
	// here so a reader of the register can tell what LINT-011 was for without
	// opening the catalogue.
	Rule string `json:"first_named"`
}

const registerNote = "Every lint finding identifier this repository has ever assigned. " +
	"Appended to by tools/lintgen and never rewritten by it. tools/lintcheck fails when an " +
	"identifier listed here is missing from engine/internal/insights/lintcatalog.yaml, which " +
	"is what makes the identifier stable rather than merely intended to be."

func main() {
	var (
		in      = flag.String("catalog", "engine/internal/insights/lintcatalog.yaml", "path to the catalogue")
		goOut   = flag.String("go", "engine/internal/insights/findings.gen.go", "path for the generated Go file")
		docOut  = flag.String("docs", "docs/src/content/docs/reference/lint-findings.md", "path for the generated reference page")
		jsonOut = flag.String("json", "www/public/lint-findings.v1.json", "path for the public machine readable catalogue")
		regOut  = flag.String("register", "engine/internal/insights/findings.register.json", "path for the identifier register")
		check   = flag.Bool("check", false, "fail if regenerating would change anything")
	)
	flag.Parse()

	if err := run(*in, *goOut, *docOut, *jsonOut, *regOut, *check); err != nil {
		fmt.Fprintln(os.Stderr, "lintgen:", err)
		os.Exit(1)
	}
}

func run(in, goOut, docOut, jsonOut, regOut string, check bool) error {
	raw, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	var c catalog
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return fmt.Errorf("parse %s: %w", in, err)
	}
	if err := validate(c.Findings); err != nil {
		return err
	}
	sort.Slice(c.Findings, func(i, j int) bool { return c.Findings[i].ID < c.Findings[j].ID })

	reg, err := nextRegister(regOut, c.Findings)
	if err != nil {
		return err
	}

	goSrc, err := renderGo(c.Findings)
	if err != nil {
		return err
	}
	regSrc, err := renderJSON(reg)
	if err != nil {
		return err
	}
	jsonSrc, err := renderJSON(publicCatalog(c.Findings))
	if err != nil {
		return err
	}

	for _, f := range []struct {
		path string
		data []byte
	}{{goOut, goSrc}, {docOut, renderDocs(c.Findings)}, {jsonOut, jsonSrc}, {regOut, regSrc}} {
		if check {
			old, readErr := os.ReadFile(f.path)
			if readErr != nil {
				return fmt.Errorf("%s is missing; run 'just generate'", f.path)
			}
			if !bytes.Equal(old, f.data) {
				return fmt.Errorf("%s is out of date; run 'just generate'", f.path)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(f.path, f.data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// validate refuses a catalogue that cannot mean what it says.
//
// A duplicate identifier is the one that matters most. Two findings sharing
// LINT-004 makes the identifier useless for exactly the thing it exists for,
// and it is the kind of mistake a copied block produces silently.
func validate(entries []entry) error {
	if len(entries) == 0 {
		return fmt.Errorf("the catalogue is empty, so this generator would erase the lint")
	}
	byID := map[string]string{}
	byRule := map[string]string{}
	for _, e := range entries {
		switch {
		case !idPattern.MatchString(e.ID):
			return fmt.Errorf("%q is not an identifier; the shape is LINT-004", e.ID)
		case e.Rule == "":
			return fmt.Errorf("%s names no rule", e.ID)
		case !rulePattern.MatchString(e.Rule):
			return fmt.Errorf("%s: %q is not a rule name; lower case, digits and underscores",
				e.ID, e.Rule)
		case e.Title == "":
			return fmt.Errorf("%s has no title, so the report would print a bare identifier", e.ID)
		}
		if first, dup := byID[e.ID]; dup {
			return fmt.Errorf("%s is assigned twice, to %s and to %s. An identifier means one "+
				"finding for as long as this project exists; take the next unused number",
				e.ID, first, e.Rule)
		}
		byID[e.ID] = e.Rule
		if e.Retired != "" {
			continue
		}
		if first, dup := byRule[e.Rule]; dup {
			return fmt.Errorf("rule %q is in the catalogue twice, as %s and as %s",
				e.Rule, first, e.ID)
		}
		byRule[e.Rule] = e.ID
	}
	return nil
}

// nextRegister returns the register with any newly assigned identifier
// appended, and refuses one that has gone missing.
func nextRegister(path string, entries []entry) (register, error) {
	reg := register{Note: registerNote}
	body, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(body, &reg); err != nil {
			return reg, fmt.Errorf("parse %s: %w", path, err)
		}
	case !os.IsNotExist(err):
		return reg, err
	}
	reg.Note = registerNote

	inCatalog := map[string]bool{}
	for _, e := range entries {
		inCatalog[e.ID] = true
	}
	known := map[string]bool{}
	var gone []string
	for _, r := range reg.Assigned {
		known[r.ID] = true
		if !inCatalog[r.ID] {
			gone = append(gone, r.ID)
		}
	}
	if len(gone) > 0 {
		sort.Strings(gone)
		return reg, fmt.Errorf(
			"%s was assigned and is no longer in the catalogue: %s. An identifier is stable "+
				"forever, so a rule that is gone keeps its entry with a 'retired:' reason "+
				"rather than losing it. Put it back",
			plural(len(gone), "identifier"), strings.Join(gone, ", "))
	}

	for _, e := range entries {
		if known[e.ID] {
			continue
		}
		reg.Assigned = append(reg.Assigned, registerEntry{ID: e.ID, Rule: e.Rule})
	}
	sort.Slice(reg.Assigned, func(i, j int) bool { return reg.Assigned[i].ID < reg.Assigned[j].ID })
	return reg, nil
}

func plural(n int, word string) string {
	if n == 1 {
		return "one " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func renderJSON(v any) ([]byte, error) {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

type publicEntry struct {
	ID      string `json:"id"`
	Rule    string `json:"rule"`
	Title   string `json:"title"`
	Retired string `json:"retired,omitempty"`
}

func publicCatalog(entries []entry) map[string]any {
	out := make([]publicEntry, 0, len(entries))
	for _, e := range entries {
		// A conversion rather than a literal: the two structs differ only in
		// their tags, so a literal would silently drop a field somebody adds
		// to one and not the other.
		out = append(out, publicEntry(e))
	}
	return map[string]any{
		"$id": "https://antifailure.dev/lint-findings.v1.json",
		"description": "Every migration lint finding Antifailure can report. The id is stable " +
			"across releases and is what a filter should match on. The rule name and the title " +
			"are prose and are rewritten whenever a clearer one exists.",
		"findings": out,
	}
}

func renderGo(entries []entry) ([]byte, error) {
	var b strings.Builder
	b.WriteString(`// Code generated by tools/lintgen. DO NOT EDIT.
//
// Source: engine/internal/insights/lintcatalog.yaml

package insights

// FindingID is the stable identifier for a lint finding.
//
// It is assigned once and never reused, including after the rule that earned
// it is deleted. Everything else about a finding is free to move: the rule
// name, the title, the detail and the fix are all prose and are improved
// whenever a clearer wording exists. Match on this.
type FindingID string

// findingIDs maps a rule to its identifier. A rule with no entry here has no
// identifier, which tools/lintcheck fails the build over.
var findingIDs = map[Rule]FindingID{
`)
	for _, e := range entries {
		if e.Retired != "" {
			continue
		}
		fmt.Fprintf(&b, "\t%q: %q,\n", e.Rule, e.ID)
	}
	b.WriteString(`}

// findingTitles is the one line summary of each rule, used as a heading in the
// report. The rationale and the fix live on the finding itself, because they
// depend on the table it found.
var findingTitles = map[Rule]string{
`)
	for _, e := range entries {
		if e.Retired != "" {
			continue
		}
		fmt.Fprintf(&b, "\t%q: %q,\n", e.Rule, e.Title)
	}
	b.WriteString(`}

// assignedFindingIDs is every identifier ever handed out, retired ones
// included, so that nothing can hand the same number out twice.
var assignedFindingIDs = []FindingID{
`)
	for _, e := range entries {
		fmt.Fprintf(&b, "\t%q,\n", e.ID)
	}
	b.WriteString(`}

// ID is the finding's stable identifier, or the empty string for a rule the
// catalogue does not know. Nothing reaches a user in that state: the build
// fails first.
func (r Rule) ID() FindingID { return findingIDs[r] }

// Title is the rule's one line summary.
func (r Rule) Title() string {
	if t, ok := findingTitles[r]; ok {
		return t
	}
	return string(r)
}

// AssignedFindingIDs returns every identifier the catalogue has ever assigned,
// in order.
func AssignedFindingIDs() []FindingID {
	return append([]FindingID(nil), assignedFindingIDs...)
}
`)
	return format.Source([]byte(b.String()))
}

func renderDocs(entries []entry) []byte {
	var b strings.Builder
	b.WriteString(`---
title: Lint findings
description: Every finding the migration lint can report, and the identifier for each one that does not change between releases.
sidebar:
  order: 9
---

The migration lint reports what a migration will do to a table the size of
production. Each finding carries an identifier of the form ` + "`LINT-NNN`" + `.

**The identifier is stable and everything else about a finding is not.** The
rule name, the title on this page, the sentence explaining what will happen and
the suggested fix are all prose, and they are rewritten whenever a clearer
wording exists. An identifier is assigned once and is never reused, including
after the rule that earned it is deleted, so something suppressing or counting
a finding should match on the identifier and nothing else.

This page is generated from ` + "`engine/internal/insights/lintcatalog.yaml`" + `, so
it cannot fall behind the code: a rule with no entry there fails the build, an
entry naming no rule fails it too, and an identifier that goes missing after it
has been handed out fails it as well.

The machine readable form is at
[antifailure.dev/lint-findings.v1.json](https://antifailure.dev/lint-findings.v1.json).

[What each finding means and what to write instead](/docs/concepts/insights) is
on the insights page, beside the rest of what a rehearsal measures.

## Findings

| Identifier | Rule name | What it found |
| --- | --- | --- |
`)
	for _, e := range entries {
		if e.Retired != "" {
			continue
		}
		fmt.Fprintf(&b, "| `%s` | `%s` | %s |\n", e.ID, e.Rule, upperFirst(e.Title))
	}

	var retired []entry
	for _, e := range entries {
		if e.Retired != "" {
			retired = append(retired, e)
		}
	}
	if len(retired) > 0 {
		b.WriteString(`
## Retired

These rules are gone. Their identifiers stay listed and stay spoken for, so
that nothing written against one quietly starts matching a different finding.

| Identifier | Rule name | Why it went |
| --- | --- | --- |
`)
		for _, e := range retired {
			fmt.Fprintf(&b, "| `%s` | `%s` | %s |\n", e.ID, e.Rule, upperFirst(e.Retired))
		}
	}
	return []byte(b.String())
}

// upperFirst capitalises a title for use in a sentence position, since the
// catalogue holds them lower case for the report, where they are printed
// mid line.
func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:] + "."
}
