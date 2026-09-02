module github.com/antifailure/antifailure/tools

go 1.26.0

toolchain go1.26.8

require (
	github.com/antifailure/antifailure/engine v0.0.0-20260827003151-4d231565e530
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.3
	gopkg.in/yaml.v3 v3.0.1
)

require (
	golang.org/x/mod v0.39.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/telemetry v0.0.0-20260811182544-a038080d80e5 // indirect
	golang.org/x/text v0.41.0 // indirect
	golang.org/x/tools v0.49.0 // indirect
	golang.org/x/vuln v1.7.0 // indirect
)

tool golang.org/x/vuln/cmd/govulncheck

// The tools module is never published and never imported by anything, so it
// pins the engine to the working tree. Without this, GOWORK=off resolves the
// require below to whatever engine snapshot the module proxy last saw, and
// scanrepo, the gate that keeps live credentials out of the repository, would
// be checking a copy of the engine that is not the one being committed.
replace github.com/antifailure/antifailure/engine => ../engine
