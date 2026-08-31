package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func load(t *testing.T) pricing {
	t.Helper()
	// The real file, not a fixture. A test against a copy of the prices would
	// keep passing after the prices it is meant to describe had gone stale.
	raw, err := os.ReadFile(filepath.Join("..", "..", "infra", "pricing.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var p pricing
	if err := yaml.Unmarshal(raw, &p); err != nil {
		t.Fatalf("infra/pricing.yaml does not parse: %v", err)
	}
	return p
}

func TestPricingFileIsUsable(t *testing.T) {
	p := load(t)
	if p.Meta.HoursPerMonth == 0 {
		t.Error("meta.hours_per_month is missing; every hourly rate depends on it")
	}
	if p.Meta.Checked == "" {
		t.Error("meta.checked is missing; a price with no date is a price nobody can decide to distrust")
	}
	if p.Postgres.ComputePerHour["B_Standard_B1ms"] == 0 {
		t.Error("no price for B_Standard_B1ms, which is the default the control plane stack uses")
	}
	if p.ContainerApps.FreeGrant.VCPUSeconds == 0 {
		t.Error("no Container Apps free grant; ignoring it overstates a small deployment by several times")
	}
}

func planFrom(t *testing.T, resources []map[string]any) plan {
	t.Helper()
	var changes []any
	for _, r := range resources {
		changes = append(changes, map[string]any{
			"address": r["address"],
			"type":    r["type"],
			"change":  map[string]any{"actions": []any{"create"}, "after": r["after"]},
		})
	}
	raw, _ := json.Marshal(map[string]any{"resource_changes": changes})
	var pl plan
	if err := json.Unmarshal(raw, &pl); err != nil {
		t.Fatal(err)
	}
	return pl
}

// The behaviour this tool exists for: a resource it does not recognise must be
// reported as UNKNOWN, never priced at zero. A confident small wrong number is
// worse than no number.
func TestUnknownResourceIsNotSilentlyFree(t *testing.T) {
	pl := planFrom(t, []map[string]any{
		{"address": "azurerm_kubernetes_cluster.k", "type": "azurerm_kubernetes_cluster", "after": map[string]any{}},
	})
	lines, total, unknowns := estimate(pl, load(t))
	if unknowns != 1 {
		t.Fatalf("unknowns = %d, want 1: an unrecognised resource must be admitted, not priced at zero", unknowns)
	}
	if total != 0 {
		t.Errorf("total = %v; an unknown resource must not contribute a made-up figure", total)
	}
	if !lines[0].unknown {
		t.Error("the line was not flagged unknown")
	}
}

func TestPostgresIsPricedFromItsSku(t *testing.T) {
	p := load(t)
	pl := planFrom(t, []map[string]any{{
		"address": "azurerm_postgresql_flexible_server.this",
		"type":    "azurerm_postgresql_flexible_server",
		"after":   map[string]any{"sku_name": "B_Standard_B1ms", "storage_mb": float64(32768)},
	}})
	_, total, unknowns := estimate(pl, p)
	if unknowns != 0 {
		t.Fatalf("B1ms should be priced, got %d unknowns", unknowns)
	}
	want := p.Postgres.ComputePerHour["B_Standard_B1ms"]*p.Meta.HoursPerMonth + 32*p.Postgres.StoragePerGBMonth
	if diff := total - want; diff > 0.01 || diff < -0.01 {
		t.Errorf("total = %.2f, want %.2f", total, want)
	}
}

// An unpriced SKU on a resource type the tool DOES know is still unknown. This
// is the near-miss: the type matches, so a careless implementation would price
// the storage and quietly charge nothing for the compute.
func TestUnknownSkuOnAKnownTypeIsUnknown(t *testing.T) {
	pl := planFrom(t, []map[string]any{{
		"address": "azurerm_postgresql_flexible_server.this",
		"type":    "azurerm_postgresql_flexible_server",
		"after":   map[string]any{"sku_name": "GP_Standard_D64s_v9", "storage_mb": float64(32768)},
	}})
	_, total, unknowns := estimate(pl, load(t))
	if unknowns != 1 {
		t.Errorf("an unpriced SKU must be unknown, got %d unknowns", unknowns)
	}
	if total != 0 {
		t.Errorf("total = %.2f; an unpriced server must not be billed for its storage alone", total)
	}
}

func TestDestroyCostsNothing(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"resource_changes": []any{map[string]any{
		"address": "azurerm_postgresql_flexible_server.this",
		"type":    "azurerm_postgresql_flexible_server",
		"change":  map[string]any{"actions": []any{"delete"}, "after": nil},
	}}})
	var pl plan
	_ = json.Unmarshal(raw, &pl)
	lines, total, _ := estimate(pl, load(t))
	if len(lines) != 0 || total != 0 {
		t.Errorf("a destroy contributed %.2f across %d lines", total, len(lines))
	}
}

func TestScaleToZeroIsFree(t *testing.T) {
	pl := planFrom(t, []map[string]any{{
		"address": "azurerm_container_app.this",
		"type":    "azurerm_container_app",
		"after": map[string]any{"template": []any{map[string]any{
			"min_replicas": float64(0),
			"container":    []any{map[string]any{"cpu": 0.5, "memory": "1Gi"}},
		}}},
	}})
	_, total, unknowns := estimate(pl, load(t))
	if unknowns != 0 || total != 0 {
		t.Errorf("min_replicas 0 should cost nothing to sit idle: total=%.2f unknowns=%d", total, unknowns)
	}
}

func TestGibParsing(t *testing.T) {
	for in, want := range map[string]float64{"1Gi": 1, "0.5Gi": 0.5, "512Mi": 0.5, "2Gi": 2} {
		if got := gib(in); got != want {
			t.Errorf("gib(%q) = %v, want %v", in, got, want)
		}
	}
}

// High availability is a second server, so it doubles the disk as well as the
// compute. The tool multiplied only compute until the production stack was
// priced, which is a quiet understatement of exactly the plan that needed the
// number most.
func TestHighAvailabilityDoublesStorageAsWellAsCompute(t *testing.T) {
	p := load(t)
	after := func(ha bool) map[string]any {
		a := map[string]any{"sku_name": "GP_Standard_D2ds_v4", "storage_mb": float64(65536)}
		if ha {
			a["high_availability"] = []any{map[string]any{"mode": "ZoneRedundant"}}
		}
		return a
	}
	single := planFrom(t, []map[string]any{{
		"address": "azurerm_postgresql_flexible_server.this",
		"type":    "azurerm_postgresql_flexible_server",
		"after":   after(false),
	}})
	paired := planFrom(t, []map[string]any{{
		"address": "azurerm_postgresql_flexible_server.this",
		"type":    "azurerm_postgresql_flexible_server",
		"after":   after(true),
	}})

	_, one, unknowns := estimate(single, p)
	if unknowns != 0 {
		t.Fatalf("GP_Standard_D2ds_v4 is the SKU high availability forces and it must be priced, got %d unknowns", unknowns)
	}
	_, two, _ := estimate(paired, p)

	if diff := two - one*p.Postgres.HighAvailabilityMult; diff > 0.01 || diff < -0.01 {
		t.Errorf("HA total = %.2f, want %.2f: the standby carries its own storage, not only its own compute",
			two, one*p.Postgres.HighAvailabilityMult)
	}
}

// An availability test is billed per execution, and an execution is one
// location running once. Three locations every five minutes is three times the
// bill of one, which is the part that surprises people.
func TestWebTestIsPricedPerLocationPerRun(t *testing.T) {
	p := load(t)
	pl := planFrom(t, []map[string]any{{
		"address": "azurerm_application_insights_standard_web_test.readyz",
		"type":    "azurerm_application_insights_standard_web_test",
		"after": map[string]any{
			"geo_locations": []any{"us-va-ash-azr", "us-ca-sjc-azr", "emea-nl-ams-azr"},
			"frequency":     float64(300),
		},
	}})
	_, total, unknowns := estimate(pl, p)
	if unknowns != 0 {
		t.Fatalf("an availability test must be priced, got %d unknowns", unknowns)
	}
	want := p.Meta.HoursPerMonth * 3600 / 300 * 3 * p.AzureMonitor.WebTestExecution
	if diff := total - want; diff > 0.01 || diff < -0.01 {
		t.Errorf("total = %.4f, want %.4f", total, want)
	}
}

// Every resource type the two stacks in this repository can create must be
// either priced or explicitly free. A type nobody taught the tool about is
// reported UNKNOWN, which suppresses the total, which is exactly what happened
// to the production plan: GP_Standard_D2ds_v4 had no price and the largest line
// in the bill came back as an admission of ignorance.
func TestEveryResourceTypeTheStacksCreateIsAccountedFor(t *testing.T) {
	p := load(t)
	types := []string{
		"azurerm_resource_group",
		"azurerm_log_analytics_workspace",
		"azurerm_consumption_budget_resource_group",
		"azurerm_virtual_network",
		"azurerm_subnet",
		"azurerm_private_dns_zone",
		"azurerm_private_dns_zone_virtual_network_link",
		"azurerm_postgresql_flexible_server_configuration",
		"azurerm_postgresql_flexible_server_database",
		"azurerm_key_vault",
		"azurerm_key_vault_secret",
		"azurerm_user_assigned_identity",
		"azurerm_role_assignment",
		"azurerm_monitor_diagnostic_setting",
		"azurerm_container_app_environment",
		"random_password",
		"random_bytes",
		"terraform_data",
		"azurerm_dns_cname_record",
		"azurerm_dns_txt_record",
		"azurerm_container_app_environment_managed_certificate",
		"azurerm_container_app_custom_domain",
		"azurerm_monitor_action_group",
		"azurerm_application_insights",
		"azurerm_monitor_metric_alert",
	}
	var resources []map[string]any
	for _, ty := range types {
		resources = append(resources, map[string]any{"address": ty + ".x", "type": ty, "after": map[string]any{}})
	}
	lines, _, unknowns := estimate(planFrom(t, resources), p)
	if unknowns != 0 {
		for _, l := range lines {
			if l.unknown {
				t.Errorf("%s: %s", l.address, l.detail)
			}
		}
		t.Fatalf("%d resource type(s) the stacks create have no price and no free entry", unknowns)
	}
}
