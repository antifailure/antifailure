package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// readLoadEvidence keeps an omitted or empty experiment out of the green streak.
// An infrastructure gap is named as such, not blamed on the application.
func (r *runner) readLoadEvidence(run *Run) {
	if !r.requireLoad {
		return
	}
	body, err := os.ReadFile(r.reportJSONPath)
	if err != nil {
		run.Green = false
		run.Findings = append(run.Findings, "Load evidence is unreadable: "+err.Error())
		return
	}
	if err := checkLoadEvidence(body); err != nil {
		run.Green = false
		run.Findings = append(run.Findings, "Load was inconclusive: "+err.Error())
	}
}

func checkLoadEvidence(body []byte) error {
	var report struct {
		Load *struct {
			Sent        int
			Unavailable string
			Routes      []struct {
				Route string
				Sent  int
			}
		}
	}
	if err := json.Unmarshal(body, &report); err != nil {
		return fmt.Errorf("invalid report: %w", err)
	}
	if report.Load == nil {
		return fmt.Errorf("the report contains no load result")
	}
	l := report.Load
	if l.Unavailable != "" {
		return fmt.Errorf("%s", l.Unavailable)
	}
	if l.Sent <= 0 {
		return fmt.Errorf("the load generator sent no requests")
	}
	total := 0
	for _, route := range l.Routes {
		if route.Route == "" || route.Sent <= 0 {
			return fmt.Errorf("route %q has no observed requests", route.Route)
		}
		total += route.Sent
	}
	if total != l.Sent {
		return fmt.Errorf("route requests total %d but load reports %d", total, l.Sent)
	}
	return nil
}
