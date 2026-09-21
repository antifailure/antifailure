package change

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The content rules for infrastructure as code.
//
// The path rule says a file is infrastructure. That is a true sentence and it
// names no check, which is why the infrastructure surface selected nothing for
// as long as the path rule was the only thing reading these files: "your
// Terraform changed" cannot be answered with "so the migration rehearsal will
// look at it", because most Terraform has nothing to do with migrations.
//
// These rules read the added LINES instead, and each one is written to the
// mechanism that makes its claim true rather than to the category the file is
// in. An engine version and a server parameter decide what a migration does
// when it runs, and the migration rehearsal is the thing that runs one. A
// replica count decides how much of the application is there to serve traffic,
// and load is the thing that sends traffic. A firewall rule decides what may be
// reached, and the egress check is where a request meets a policy.
//
// What they deliberately do not do is guess. Every rule here matches a
// declaration a person wrote, names it as the fact's subject, and carries the
// line number it was found on, so a wrong conclusion can be opened and
// corrected. Nothing here parses HCL: a parser for one syntax would answer
// nothing about the Kubernetes manifests, Helm values, Bicep and CloudFormation
// that the same path rule claims, and a rule that works on one of five formats
// reads in the report exactly like a rule that works.

// MaxIaCSubjectsPerFile bounds how many distinct infrastructure subjects one
// file contributes, for the same reason MaxHostsPerFile bounds the hosts: a
// checked in list of two hundred server parameters must not produce a profile
// longer than the diff it describes.
const MaxIaCSubjectsPerFile = 20

// dbVersionKeys are the keys that declare a database engine's version.
//
// A bare `version` is deliberately absent, and this is the most consequential
// omission in this file. Azure writes the Postgres major as `version` inside
// azurerm_postgresql_flexible_server, so a version bump there is not seen. The
// alternative is worse: `version` is also the key on every provider pin, every
// Helm chart, every API version line in every Kubernetes manifest, and reading
// it would select the migration rehearsal on a chart bump. A stated limit, said
// out loud in the documentation, beats a rule that is right once and noisy a
// hundred times.
var dbVersionKeys = map[string]bool{
	"engine_version":     true,
	"database_version":   true,
	"postgres_version":   true,
	"postgresql_version": true,
	"db_version":         true,
	"server_version":     true,
	"mysql_version":      true,
}

// serverParameters are the Postgres settings that decide what a statement does
// when it runs: how long it waits for a lock, when it gives up, how much memory
// it may use to sort, how many connections there are to take.
//
// They are matched as a KEY and as a VALUE, because the two shapes
// infrastructure writes them in put them on opposite sides of the equals sign.
// A postgresql.conf line or an Azure configuration resource writes
// `lock_timeout = "5s"`; an AWS parameter group and a Cloud SQL database flag
// write `name = "lock_timeout"` with the setting as the value of a key called
// name. A rule that read only keys would see the first and miss the second, and
// the second is the common one in Terraform.
var serverParameters = map[string]bool{
	"lock_timeout":                        true,
	"statement_timeout":                   true,
	"idle_in_transaction_session_timeout": true,
	"deadlock_timeout":                    true,
	"max_connections":                     true,
	"max_locks_per_transaction":           true,
	"max_pred_locks_per_transaction":      true,
	"shared_buffers":                      true,
	"work_mem":                            true,
	"maintenance_work_mem":                true,
	"effective_cache_size":                true,
	"max_wal_size":                        true,
	"min_wal_size":                        true,
	"wal_level":                           true,
	"synchronous_commit":                  true,
	"default_transaction_isolation":       true,
	"default_transaction_read_only":       true,
	"random_page_cost":                    true,
	"autovacuum":                          true,
	"autovacuum_vacuum_scale_factor":      true,
	"max_standby_streaming_delay":         true,
	"max_worker_processes":                true,
	"max_parallel_workers":                true,
	"shared_preload_libraries":            true,
	"log_min_duration_statement":          true,
}

// capacityKeys declare how much of the application there is: how many copies,
// how large each one is, and how far an autoscaler may move either number.
//
// Matched as keys only. `cpu`, `memory` and `workers` are ordinary English and
// would match prose anywhere; as the left hand side of an assignment inside a
// file the path rule already called infrastructure, they are a size.
var capacityKeys = map[string]bool{
	"desired_count":         true,
	"replicas":              true,
	"replica_count":         true,
	"min_replicas":          true,
	"max_replicas":          true,
	"min_capacity":          true,
	"max_capacity":          true,
	"min_size":              true,
	"max_size":              true,
	"min_count":             true,
	"max_count":             true,
	"min_instances":         true,
	"max_instances":         true,
	"min_instance_count":    true,
	"max_instance_count":    true,
	"node_count":            true,
	"instance_class":        true,
	"instance_type":         true,
	"machine_type":          true,
	"sku_name":              true,
	"cpu":                   true,
	"memory":                true,
	"workers":               true,
	"worker_count":          true,
	"allocated_storage":     true,
	"max_allocated_storage": true,
	"disk_size":             true,
	"disk_size_gb":          true,
	"read_replica_count":    true,
}

// networkKeys are the attributes a firewall, security group or network policy
// rule is written with.
var networkKeys = map[string]bool{
	"cidr_blocks":                  true,
	"ipv6_cidr_blocks":             true,
	"source_ranges":                true,
	"destination_ranges":           true,
	"source_address_prefix":        true,
	"destination_address_prefix":   true,
	"source_address_prefixes":      true,
	"destination_address_prefixes": true,
	"source_security_group_id":     true,
	"security_group_ids":           true,
	"vpc_security_group_ids":       true,
	"from_port":                    true,
	"to_port":                      true,
	"start_ip_address":             true,
	"end_ip_address":               true,
	"allowed_ips":                  true,
	"ip_ranges":                    true,
	"publicly_accessible":          true,
}

// networkResources are the resource types whose whole purpose is a network
// rule. They are matched as a token anywhere on the line, because the line that
// declares one is `resource "aws_security_group_rule" "db" {` and has no
// assignment on it at all.
var networkResources = map[string]bool{
	"aws_security_group":                               true,
	"aws_security_group_rule":                          true,
	"aws_vpc_security_group_ingress_rule":              true,
	"aws_vpc_security_group_egress_rule":               true,
	"aws_network_acl_rule":                             true,
	"aws_db_security_group":                            true,
	"google_compute_firewall":                          true,
	"azurerm_network_security_rule":                    true,
	"azurerm_network_security_group":                   true,
	"azurerm_postgresql_flexible_server_firewall_rule": true,
	"azurerm_firewall_network_rule_collection":         true,
	"networkpolicy":                                    true,
}

// assignmentPattern reads the left and right of one `key = value`, `key: value`
// or `"key": value`, in HCL, YAML and JSON alike, with a YAML list item's
// leading dash allowed in front of the key.
//
// Deliberately not a parser. Five syntaxes are claimed by the same path rule,
// and a parser for one of them would answer nothing about the other four while
// looking in the report exactly like a rule that works.
var assignmentPattern = regexp.MustCompile(`^[\s-]*"?([A-Za-z_][A-Za-z0-9_.-]*)"?\s*[:=]\s*(.*)$`)

// identifierPattern finds the identifier tokens on a line, which is how a
// resource type is recognised on a line that assigns nothing.
var identifierPattern = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

// versionNumber finds the first run of digits in a declared version, which is
// the major: 16 out of "16.1", out of "POSTGRES_16" and out of "15.4".
var versionNumber = regexp.MustCompile(`\d+`)

// iacFacts reports what an infrastructure file's added lines say about the
// database, the capacity and the network.
//
// Called only for a file the path rules classified as infrastructure, which is
// what keeps `replicas` in a Go file from being read as a fleet size. The facts
// it returns sit BESIDE that infrastructure fact rather than replacing it, the
// way an outbound host sits beside the code fact on the file that names it.
func iacFacts(f File, m *schema.Manifest) []Fact {
	if f.Binary {
		return nil
	}

	type found struct {
		rule    string
		surface Surface
		subject string
		why     string
		line    int
	}
	seen := map[string]bool{}
	var out []found

	add := func(rule string, surface Surface, subject, why string, line int) {
		key := rule + "\x00" + subject
		if seen[key] || len(out) >= MaxIaCSubjectsPerFile {
			return
		}
		seen[key] = true
		out = append(out, found{rule, surface, subject, why, line})
	}

	// Two guards on the cap, and only the one inside add is the bound.
	//
	// This one is a SHORT CIRCUIT: it stops reading the rest of a long file once
	// enough subjects exist, and it can never make the count exact because it is
	// checked before a line rather than between the subjects that line produces.
	// One line can produce two, `cidr_blocks = [aws_security_group.api.id]`
	// being an attribute and a resource type at once, so arriving here with one
	// slot left and taking two is what add refuses. A mutation aimed at the cap
	// survives against this guard and reds against that one, which is the
	// difference worth knowing before deleting either.
	for _, added := range f.AddedLines {
		if len(out) >= MaxIaCSubjectsPerFile {
			break
		}
		line := stripComment(added.Text)
		key, value, isAssignment := iacAssignment(line)

		if isAssignment && dbVersionKeys[key] {
			if major := versionNumber.FindString(value); major != "" {
				add("content.iac_database_version", SurfaceDatabaseConfig,
					major, databaseVersionEvidence(major, m), added.N)
			}
		}
		if isAssignment {
			switch {
			case serverParameters[key]:
				add("content.iac_server_parameter", SurfaceDatabaseConfig,
					key, serverParameterEvidence(key), added.N)
			case serverParameters[strings.ToLower(value)]:
				// The AWS parameter group and the Cloud SQL database flag
				// shape, where the setting is the VALUE of a key called name.
				add("content.iac_server_parameter", SurfaceDatabaseConfig,
					strings.ToLower(value), serverParameterEvidence(strings.ToLower(value)), added.N)
			}
			if capacityKeys[key] {
				add("content.iac_capacity", SurfaceCapacity, key, capacityEvidence(key), added.N)
			}
			if networkKeys[key] {
				add("content.iac_network_rule", SurfaceNetworkRule, key, networkEvidence(key), added.N)
			}
		}
		for _, token := range identifierPattern.FindAllString(strings.ToLower(line), -1) {
			if networkResources[token] {
				add("content.iac_network_rule", SurfaceNetworkRule, token, networkEvidence(token), added.N)
			}
		}
	}

	// Sorted by rule then subject so that the same file produces the same facts
	// in the same order forever, which is what a report diffed against an
	// earlier one rests on. The analyser sorts every fact again afterwards;
	// this is so that the MaxIaCSubjectsPerFile cut is not the only thing
	// deciding which twenty a long file contributes.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].rule != out[j].rule {
			return out[i].rule < out[j].rule
		}
		return out[i].subject < out[j].subject
	})

	facts := make([]Fact, 0, len(out))
	for _, v := range out {
		facts = append(facts, Fact{
			Path: f.Path, Status: f.Status, Surface: v.surface, Subject: v.subject,
			Rule: v.rule, Evidence: v.why, Line: v.line,
		})
	}
	return facts
}

// iacAssignment splits one line into the key it sets and the value it sets it
// to. The value is returned unquoted and without its trailing punctuation.
func iacAssignment(line string) (string, string, bool) {
	match := assignmentPattern.FindStringSubmatch(line)
	if match == nil {
		return "", "", false
	}
	value := strings.TrimSpace(match[2])
	value = strings.Trim(value, `,;`)
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"'`)
	return strings.ToLower(match[1]), value, true
}

// stripComment removes a trailing line comment in the two forms every one of
// these syntaxes uses.
//
// It protects the RESOURCE TOKEN SCAN and only that, which is worth writing
// down because it is not what it looks like it protects. The assignment rules
// cannot see a comment in the first place: a line beginning with `#` or `//`
// has no key in front of its separator, so the pattern refuses it with or
// without this. The token scan reads the whole line, because that is how a
// `resource "aws_security_group_rule" "db" {` line is recognised when it
// assigns nothing, and without this a trailing comment mentioning one would be
// read as a rule the diff declared.
//
// So the case to aim a test at is `name = "api" # replaced
// aws_security_group_rule`, and a test built on a line that is nothing but a
// comment stays green with this deleted. That is how the first version of the
// test next to this was found to be measuring nothing.
func stripComment(line string) string {
	if i := strings.Index(line, "//"); i >= 0 {
		line = line[:i]
	}
	if i := strings.Index(line, "#"); i >= 0 {
		line = line[:i]
	}
	return line
}

// databaseVersionEvidence says what the rehearsal will actually run against,
// which is the manifest's version and not this one.
//
// The comparison is the point. A pull request that moves production to Postgres
// 16 while antifailure.yaml still says 15 is rehearsed against 15, and before
// this sentence existed nothing anywhere said so: the diff knew one number, the
// manifest knew the other, and no reader held both.
//
// The default is named as "the engine's default" rather than as a number on
// purpose. Which version an unset manifest gets is decided by databaseVersion
// in internal/env, and a second copy of that constant here would be a second
// opinion about one fact, which is the shape of bug that takes a year to find.
func databaseVersionEvidence(major string, m *schema.Manifest) string {
	said := "an added line sets a database engine version of " + major
	if m == nil || m.Database == nil || m.Database.Version == 0 {
		return said + ", and the manifest declares no database version, so the migration rehearsal " +
			"applies this change's migrations to the engine's default rather than to this one"
	}
	declared := strconv.Itoa(m.Database.Version)
	if declared == major {
		return said + ", which is the version the manifest declares, so the migration rehearsal " +
			"applies this change's migrations to it"
	}
	return said + " and the manifest declares " + declared +
		", so the migration rehearsal applies this change's migrations to " + declared +
		" and not to " + major + ". Whether this is the database the manifest means is not visible from a diff"
}

func serverParameterEvidence(name string) string {
	return "an added line names the server parameter " + name +
		", which governs what a statement does against this database, and the migration rehearsal " +
		"is what runs this change's statements against one"
}

func capacityEvidence(key string) string {
	return "an added line sets " + key +
		", which is how much of the application is there to serve traffic, and load is the check " +
		"that puts production shaped traffic through it"
}

func networkEvidence(subject string) string {
	return "an added line declares the network rule " + subject +
		", which decides what this application may reach and what may reach it, and the egress check " +
		"is where an outbound request meets the policy and gets a decision"
}
