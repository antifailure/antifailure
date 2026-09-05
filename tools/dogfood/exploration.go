package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

func (r *runner) readExplorationEvidence(run *Run) {
	refuse := func(reason string) {
		run.Green = false
		run.Findings = append(run.Findings, "Exploration evidence could not be verified: "+reason+". The experiment is inconclusive, not an application failure.")
	}
	var manifest struct {
		Explore *struct {
			Enabled bool
			Goals   []struct{ Name string }
		}
	}
	body, err := os.ReadFile(filepath.Join(r.root, "antifailure.yaml"))
	if err != nil {
		refuse("the manifest could not be read")
		return
	}
	if err = yaml.Unmarshal(body, &manifest); err != nil {
		refuse("the manifest could not be decoded")
		return
	}
	if manifest.Explore == nil || !manifest.Explore.Enabled {
		return
	}
	var doc struct {
		Exploration *struct {
			Unavailable string
			Results     []struct {
				Name    string   `json:"name"`
				Visited []string `json:"visited"`
				Outcome struct {
					Verdict string `json:"verdict"`
				} `json:"outcome"`
				Evidence struct {
					Trace string `json:"trace"`
				} `json:"evidence"`
			}
		}
	}
	body, err = os.ReadFile(r.reportJSONPath)
	if err != nil {
		refuse("the JSON report could not be read")
		return
	}
	if err = json.Unmarshal(body, &doc); err != nil || doc.Exploration == nil {
		refuse("the report has no readable exploration result")
		return
	}
	if doc.Exploration.Unavailable != "" {
		refuse(doc.Exploration.Unavailable)
		return
	}
	seen := map[string]bool{}
	for _, x := range doc.Exploration.Results {
		if seen[x.Name] || x.Outcome.Verdict != "pass" || len(x.Visited) == 0 || x.Evidence.Trace == "" {
			refuse(fmt.Sprintf("goal %q has no complete browser evidence", x.Name))
			return
		}
		seen[x.Name] = true
	}
	for _, g := range manifest.Explore.Goals {
		if !seen[g.Name] {
			refuse(fmt.Sprintf("goal %q did not run", g.Name))
			return
		}
	}
	if len(seen) != len(manifest.Explore.Goals) || len(seen) == 0 {
		refuse("the results do not match the declared goals")
	}
}
