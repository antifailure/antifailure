// Command azguard refuses an Azure operation whose target is not Antifailure's.
//
// This subscription holds other people's work. At the time this was written it
// carried Ravioli, bonfire, NetworkWatcherRG, ME_bonfire-aca-env_bonfire_eastus
// and postiz-rg, none of which belongs to this project. The boundary in
// infra/ISOLATION.md says Antifailure creates and touches resource groups
// prefixed af- and tagged project=antifailure, and nothing else.
//
// A boundary that lives only in a document is a boundary somebody crosses at
// two in the morning. This is the executable half. It is deliberately small and
// deliberately paranoid: it fails closed, so an error talking to Azure is a
// refusal rather than an assumption that the target was fine.
//
//	azguard check af-cp-centralus         # is this group ours?
//	azguard check --tags af-cp-centralus  # ...and does it carry the tag?
//	azguard guard -- terraform apply      # refuse unless every -var group is ours
//	azguard region centralus              # can this region actually run the database?
//
// Exit codes: 0 allowed, 1 refused, 2 the guard could not tell.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// requiredPrefix and requiredTag are the whole boundary, in two constants.
const (
	requiredPrefix = "af-"
	requiredTagKey = "project"
	requiredTagVal = "antifailure"
)

// knownForeign are the groups that existed in this subscription before
// Antifailure did. They are listed by name, not to make the check work (the
// prefix rule already refuses them) but so that a refusal can say WHICH
// project's resources it just protected. A named refusal gets read; a generic
// one gets overridden.
var knownForeign = map[string]string{
	"ravioli":                           "Ravioli",
	"bonfire":                           "bonfire",
	"networkwatcherrg":                  "NetworkWatcherRG",
	"me_bonfire-aca-env_bonfire_eastus": "the bonfire Container Apps environment",
	"postiz-rg":                         "postiz",
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "check":
		os.Exit(cmdCheck(os.Args[2:]))
	case "guard":
		os.Exit(cmdGuard(os.Args[2:]))
	case "region":
		os.Exit(cmdRegion(os.Args[2:]))
	case "-h", "--help", "help":
		usage()
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "azguard: unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `azguard: refuse an Azure operation outside Antifailure's resource groups.

  azguard check [--tags] <resource-group>...
      Refuse unless every group is named af-* . With --tags, also require the
      group to exist and carry project=antifailure, which needs Azure access.

  azguard guard [--tags] -- <command> [args...]
      Extract every resource group named in the command (-g, --resource-group,
      and Terraform's -var resource_group_name=) , check them, and run the
      command only if every one is ours.

  azguard region [--postgres-version V] [--postgres-sku S] <location>...
      Ask Azure whether a region can actually create this stack's PostgreSQL
      flexible server. This is a THIRD gate, separate from quota and from Azure
      Policy, and neither a plan nor a policy can see it: eastus on this
      subscription returns supportedServerVersions: [] with "Provisioning is
      restricted in this region", so an apply gets twenty six resources in and
      then fails on the database.

Exit codes: 0 allowed, 1 refused, 2 could not tell.
`)
}

// nameIsOurs is the offline half of the check: no network, no credentials, and
// therefore no reason for anything to skip it.
func nameIsOurs(group string) error {
	if strings.TrimSpace(group) == "" {
		return errors.New("empty resource group name")
	}
	if owner, ok := knownForeign[strings.ToLower(group)]; ok {
		return fmt.Errorf("%s belongs to %s, not to Antifailure. ISOLATION.md lists it as a group this project never touches", group, owner)
	}
	// AZURE CREATES RESOURCE GROUPS THIS PROJECT DID NOT ASK FOR, and refusing
	// them with "belongs to another project" would be a confident, wrong answer.
	//
	// A Container Apps environment produces a second group called
	// ME_<environment>_<group>_<location> holding the platform's own
	// infrastructure. It is created and deleted by Azure, its `managedBy` points
	// back at the environment, and it inherits the environment's tags, so a
	// cleanup scoped to project=antifailure DOES reach it even though the name
	// rule cannot. Deleting the environment deletes it; touching it directly is
	// how a running environment gets broken.
	//
	// This is still a REFUSAL. The message is the whole difference: it says why
	// the group exists and what to operate on instead.
	if strings.HasPrefix(group, "ME_") {
		return fmt.Errorf("%s is created and owned by Azure, not by this project: it holds the infrastructure for a Container Apps environment and its lifecycle belongs to that environment. Delete the environment instead. It inherits the environment's tags, so a cleanup scoped to %s=%s reaches it even though its name cannot start with %s",
			group, requiredTagKey, requiredTagVal, requiredPrefix)
	}
	if !strings.HasPrefix(group, requiredPrefix) {
		return fmt.Errorf("%s is not prefixed %q. Antifailure creates and operates on resource groups it owns and nothing else; this subscription holds other projects' groups", group, requiredPrefix)
	}
	return nil
}

// tagIsOurs is the online half. It fails closed: if Azure cannot be reached, or
// the group does not exist, the answer is "could not tell", which is a refusal
// and not a pass.
func tagIsOurs(group string) error {
	out, err := exec.Command("az", "group", "show", "--name", group, "--query", "tags", "-o", "json").Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return fmt.Errorf("could not read the tags on %s: %s", group, strings.TrimSpace(string(ee.Stderr)))
		}
		return fmt.Errorf("could not run az to read the tags on %s: %w", group, err)
	}
	var tags map[string]string
	if err := json.Unmarshal(out, &tags); err != nil {
		return fmt.Errorf("could not parse the tags on %s: %w", group, err)
	}
	if got := tags[requiredTagKey]; got != requiredTagVal {
		return fmt.Errorf("%s is not tagged %s=%s (it has %s=%q). An untagged group is one a scoped cleanup would miss, and one this project may not have created",
			group, requiredTagKey, requiredTagVal, requiredTagKey, got)
	}
	return nil
}

func check(groups []string, withTags bool) []error {
	var errs []error
	for _, g := range groups {
		if err := nameIsOurs(g); err != nil {
			errs = append(errs, err)
			continue
		}
		if withTags {
			if err := tagIsOurs(g); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errs
}

func cmdCheck(args []string) int {
	withTags, groups := splitTagsFlag(args)
	if len(groups) == 0 {
		fmt.Fprintln(os.Stderr, "azguard check: no resource group given")
		return 2
	}
	if errs := check(groups, withTags); len(errs) > 0 {
		report(errs)
		return 1
	}
	fmt.Printf("azguard: %s\n", strings.Join(groups, ", "))
	return 0
}

func cmdGuard(args []string) int {
	withTags, rest := splitTagsFlag(args)
	if i := indexOf(rest, "--"); i >= 0 {
		rest = rest[i+1:]
	}
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "azguard guard: no command given after --")
		return 2
	}

	groups := groupsIn(rest)
	if len(groups) == 0 {
		// Fails closed. A command naming no group might be harmless, and it
		// might be `az group delete` reading a name from somewhere this guard
		// cannot see. Refusing is recoverable; guessing is not.
		fmt.Fprintf(os.Stderr, "azguard: refusing %q because it names no resource group this guard can identify.\n", strings.Join(rest, " "))
		fmt.Fprintln(os.Stderr, "  Name the group explicitly (-g / --resource-group / -var resource_group_name=) so the boundary can be checked.")
		return 1
	}
	if errs := check(groups, withTags); len(errs) > 0 {
		report(errs)
		fmt.Fprintf(os.Stderr, "\nrefused: %s\n", strings.Join(rest, " "))
		return 1
	}

	cmd := exec.Command(rest[0], rest[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "azguard: %v\n", err)
		return 2
	}
	return 0
}

// groupsIn pulls every resource group name out of a command line, in the forms
// the az CLI and Terraform actually use.
func groupsIn(args []string) []string {
	var found []string
	add := func(v string) {
		if v != "" {
			found = append(found, v)
		}
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() string {
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		switch {
		case a == "-g" || a == "--resource-group":
			add(next())
		case strings.HasPrefix(a, "--resource-group="):
			add(strings.TrimPrefix(a, "--resource-group="))
		case a == "-var":
			add(varGroup(next()))
		case strings.HasPrefix(a, "-var="):
			add(varGroup(strings.TrimPrefix(a, "-var=")))
		}
	}
	return found
}

// varGroup reads `resource_group_name=af-cp-scus`, with or without the quotes
// a shell may have left behind.
func varGroup(v string) string {
	k, val, ok := strings.Cut(v, "=")
	if !ok || strings.TrimSpace(k) != "resource_group_name" {
		return ""
	}
	return strings.Trim(strings.TrimSpace(val), `"'`)
}

func splitTagsFlag(args []string) (bool, []string) {
	withTags := false
	var rest []string
	for _, a := range args {
		if a == "--tags" {
			withTags = true
			continue
		}
		rest = append(rest, a)
	}
	return withTags, rest
}

func indexOf(xs []string, want string) int {
	for i, x := range xs {
		if x == want {
			return i
		}
	}
	return -1
}

func report(errs []error) {
	fmt.Fprintln(os.Stderr, "azguard: refused.")
	for _, e := range errs {
		fmt.Fprintf(os.Stderr, "  %v\n", e)
	}
	fmt.Fprintln(os.Stderr, "\n  The boundary is in infra/ISOLATION.md. It is not advisory.")
}
