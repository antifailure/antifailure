// Command cost estimates what a Terraform plan will cost per month.
//
//	terraform show -json plan.tfplan > plan.json
//	go run ./tools/cost estimate --plan plan.json --pricing infra/pricing.yaml
//
// The point is to see the number BEFORE the apply, because the alternative is
// seeing it on an invoice four weeks later. `--budget` makes it a gate: it
// exits non-zero when the projection exceeds the resource group's budget, which
// is what ISOLATION.md means by refusing to apply something too expensive.
//
// THE DESIGN DECISION WORTH KNOWING: a resource this tool cannot price is
// reported as UNKNOWN and, by default, makes the whole estimate refuse to
// claim a total. An estimator that silently prices unrecognised resources at
// zero is worse than no estimator, because it produces a confident, small,
// wrong number. Free things are listed explicitly as free; everything else that
// is not in pricing.yaml is an admission of ignorance.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type pricing struct {
	Meta struct {
		Currency      string  `yaml:"currency"`
		Region        string  `yaml:"region"`
		Checked       string  `yaml:"checked"`
		HoursPerMonth float64 `yaml:"hours_per_month"`
	} `yaml:"meta"`
	Postgres struct {
		ComputePerHour       map[string]float64 `yaml:"compute_per_hour"`
		StoragePerGBMonth    float64            `yaml:"storage_per_gb_month"`
		BackupLRSPerGBMonth  float64            `yaml:"backup_lrs_per_gb_month"`
		HighAvailabilityMult float64            `yaml:"high_availability_multiplier"`
	} `yaml:"postgres_flexible"`
	ContainerApps struct {
		VCPUSecondActive      float64 `yaml:"vcpu_second_active"`
		VCPUSecondIdle        float64 `yaml:"vcpu_second_idle"`
		MemoryGiBSecondActive float64 `yaml:"memory_gib_second_active"`
		MemoryGiBSecondIdle   float64 `yaml:"memory_gib_second_idle"`
		RequestsPerMillion    float64 `yaml:"requests_per_million"`
		FreeGrant             struct {
			VCPUSeconds      float64 `yaml:"vcpu_seconds"`
			MemoryGiBSeconds float64 `yaml:"memory_gib_seconds"`
			Requests         float64 `yaml:"requests"`
		} `yaml:"free_grant"`
	} `yaml:"container_apps"`
	KeyVault struct {
		OperationsPer10k float64 `yaml:"operations_per_10k"`
	} `yaml:"key_vault"`
	Storage struct {
		HotLRSPerGBMonth float64 `yaml:"hot_lrs_per_gb_month"`
		HotGRSPerGBMonth float64 `yaml:"hot_grs_per_gb_month"`
	} `yaml:"storage"`
	LogAnalytics struct {
		IngestionPerGB    float64 `yaml:"ingestion_per_gb"`
		RetentionPerGBMon float64 `yaml:"retention_per_gb_month"`
		FreeIngestionGB   float64 `yaml:"free_ingestion_gb"`
	} `yaml:"log_analytics"`
	AzureMonitor struct {
		MetricAlertPerMonth float64 `yaml:"metric_alert_per_month"`
		WebTestExecution    float64 `yaml:"web_test_execution"`
	} `yaml:"azure_monitor"`
	Assumptions struct {
		LogAnalyticsGBPerMonth float64 `yaml:"log_analytics_gb_per_month"`
		KeyVaultOpsPerMonth    float64 `yaml:"key_vault_operations_per_month"`
		GoldenStorageGB        float64 `yaml:"golden_storage_gb"`
		ContainerAppsRequests  float64 `yaml:"container_apps_requests_per_month"`
		ContainerAppsDutyCycle float64 `yaml:"container_apps_duty_cycle"`
	} `yaml:"assumptions"`
}

// plan is the subset of `terraform show -json` this needs.
type plan struct {
	ResourceChanges []struct {
		Address string `json:"address"`
		Type    string `json:"type"`
		Change  struct {
			Actions []string       `json:"actions"`
			After   map[string]any `json:"after"`
		} `json:"change"`
	} `json:"resource_changes"`
}

type line struct {
	address string
	detail  string
	monthly float64
	unknown bool
}

// free lists the resource types that genuinely cost nothing to exist. Written
// out so that "this is free" is a claim somebody made on purpose, and is
// distinguishable from "this tool has never heard of it".
var free = map[string]string{
	"azurerm_resource_group":                        "a resource group is free; what is in it is not",
	"azurerm_virtual_network":                       "no charge for the VNet itself",
	"azurerm_subnet":                                "",
	"azurerm_private_dns_zone_virtual_network_link": "",
	"azurerm_user_assigned_identity":                "",
	"azurerm_role_assignment":                       "",
	"azurerm_consumption_budget_resource_group":     "",
	"azurerm_postgresql_flexible_server_database":   "billed on the server, not per database",
	"azurerm_storage_container":                     "billed on the account, not per container",
	"azurerm_monitor_diagnostic_setting":            "the setting is free; the ingestion it causes is priced on the workspace",
	"azurerm_container_app_environment":             "the environment is free on the consumption plan; the apps in it are not",
	"azurerm_key_vault_secret":                      "storing a secret is free; the operations that read it are counted on the vault",
	"random_password":                               "",
	"random_bytes":                                  "",
	"terraform_data":                                "",

	// Missing until the production stack was written, and each of them was
	// reported UNKNOWN on a plan that has been run since the beginning:
	// random_bytes is the provider key sealing secret and the server
	// configuration is the azure.extensions allow-list without which the first
	// migration is refused.
	"azurerm_postgresql_flexible_server_configuration": "a server parameter is free; the server is not",

	// The public name. A record SET costs nothing; the zone that holds it is
	// billed per month and per million queries, and that zone is in af-web with
	// the marketing site rather than in this stack.
	"azurerm_dns_cname_record": "record sets are free; the zone they live in is billed in the group that owns it",
	"azurerm_dns_txt_record":   "",

	// Container Apps issues and renews a managed certificate at no charge, and
	// binding a domain to an app is not itself billed.
	"azurerm_container_app_environment_managed_certificate": "a managed certificate is free; a certificate you upload is too",
	"azurerm_container_app_custom_domain":                   "",

	// Alerting. The group is free and so are its first 1000 emails and 100 SMS
	// a month, which this tool does not try to model: the rules and the probes
	// below are where the money is.
	"azurerm_monitor_action_group": "the group is free; the notifications it sends have their own free grants",
	"azurerm_application_insights": "the component is free; the availability results it stores are ingestion on the workspace",
}

func main() {
	if len(os.Args) < 2 || os.Args[1] != "estimate" {
		fmt.Fprintln(os.Stderr, "usage: cost estimate --plan plan.json --pricing infra/pricing.yaml [--budget N] [--allow-unknown]")
		os.Exit(2)
	}
	// The standard library rather than pflag: this needs four flags and the
	// repository would rather not carry a dependency for that. `flag` accepts
	// both -flag and --flag, so the usage above is unchanged.
	fs := flag.NewFlagSet("estimate", flag.ExitOnError)
	planPath := fs.String("plan", "", "terraform show -json output")
	pricingPath := fs.String("pricing", "infra/pricing.yaml", "unit prices")
	budget := fs.Float64("budget", 0, "refuse if the projection exceeds this many USD per month")
	allowUnknown := fs.Bool("allow-unknown", false, "report a total even though some resources could not be priced")
	_ = fs.Parse(os.Args[2:])

	if *planPath == "" {
		fmt.Fprintln(os.Stderr, "cost: --plan is required")
		os.Exit(2)
	}

	var p pricing
	raw, err := os.ReadFile(*pricingPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cost: %v\n", err)
		os.Exit(2)
	}
	if err := yaml.Unmarshal(raw, &p); err != nil {
		fmt.Fprintf(os.Stderr, "cost: %s: %v\n", *pricingPath, err)
		os.Exit(2)
	}
	if p.Meta.HoursPerMonth == 0 {
		fmt.Fprintf(os.Stderr, "cost: %s has no meta.hours_per_month\n", *pricingPath)
		os.Exit(2)
	}

	var pl plan
	raw, err = os.ReadFile(*planPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cost: %v\n", err)
		os.Exit(2)
	}
	if err := json.Unmarshal(raw, &pl); err != nil {
		fmt.Fprintf(os.Stderr, "cost: %s: %v\n", *planPath, err)
		os.Exit(2)
	}

	lines, total, unknowns := estimate(pl, p)

	fmt.Printf("Projected monthly cost, %s, %s prices checked %s\n\n",
		p.Meta.Region, p.Meta.Currency, p.Meta.Checked)
	sort.Slice(lines, func(i, j int) bool { return lines[i].monthly > lines[j].monthly })
	for _, l := range lines {
		if l.unknown {
			fmt.Printf("  %10s  %-52s %s\n", "UNKNOWN", l.address, l.detail)
			continue
		}
		if l.monthly == 0 && l.detail != "" {
			fmt.Printf("  %10s  %-52s %s\n", "free", l.address, l.detail)
			continue
		}
		fmt.Printf("  %9.2f   %-52s %s\n", l.monthly, l.address, l.detail)
	}

	fmt.Printf("\n  %9.2f   TOTAL per month\n", total)

	if unknowns > 0 {
		fmt.Fprintf(os.Stderr, "\ncost: %d resource(s) could not be priced. The total above EXCLUDES them and is therefore a floor, not an estimate.\n", unknowns)
		fmt.Fprintln(os.Stderr, "Add them to infra/pricing.yaml, or pass --allow-unknown if you have decided they are free.")
		if !*allowUnknown {
			os.Exit(1)
		}
	}

	if *budget > 0 && total > *budget {
		fmt.Fprintf(os.Stderr, "\ncost: %.2f per month exceeds the budget of %.2f. Refusing.\n", total, *budget)
		os.Exit(1)
	}
}

func estimate(pl plan, p pricing) (lines []line, total float64, unknowns int) {
	hours := p.Meta.HoursPerMonth
	seconds := hours * 3600

	for _, rc := range pl.ResourceChanges {
		// Only what is being created or kept. A destroy costs nothing to run.
		if !creates(rc.Change.Actions) {
			continue
		}
		after := rc.Change.After
		l := line{address: rc.Address}

		switch rc.Type {
		case "azurerm_postgresql_flexible_server":
			sku, _ := after["sku_name"].(string)
			rate, ok := p.Postgres.ComputePerHour[sku]
			if !ok {
				l.unknown, l.detail = true, fmt.Sprintf("no price for sku_name %q in pricing.yaml", sku)
				break
			}
			compute := rate * hours
			storageGB := num(after["storage_mb"]) / 1024
			storage := storageGB * p.Postgres.StoragePerGBMonth
			// Zone redundant HA is a SECOND SERVER, with its own compute and
			// its own disk, and Azure bills both. This multiplied compute only
			// until the production stack was priced, which understated a
			// 64 GB production plan by 8.32 a month.
			ha, _ := after["high_availability"].([]any)
			highlyAvailable := len(ha) > 0 && p.Postgres.HighAvailabilityMult > 0
			if highlyAvailable {
				compute *= p.Postgres.HighAvailabilityMult
				storage *= p.Postgres.HighAvailabilityMult
			}
			l.monthly = compute + storage
			l.detail = fmt.Sprintf("%s, %.0f GB", sku, storageGB)
			if highlyAvailable {
				l.detail += fmt.Sprintf(", x%g for zone redundant HA", p.Postgres.HighAvailabilityMult)
			}

		case "azurerm_container_app":
			cpu, mem, replicas := containerShape(after)
			if replicas == 0 {
				l.detail = "min_replicas 0: scales to nothing when idle, billed only while serving"
				l.monthly = 0
				break
			}
			duty := p.Assumptions.ContainerAppsDutyCycle
			vcpuSec := cpu * replicas * seconds
			memSec := mem * replicas * seconds
			billVCPU := max0(vcpuSec - p.ContainerApps.FreeGrant.VCPUSeconds)
			billMem := max0(memSec - p.ContainerApps.FreeGrant.MemoryGiBSeconds)
			l.monthly = billVCPU*(duty*p.ContainerApps.VCPUSecondActive+(1-duty)*p.ContainerApps.VCPUSecondIdle) +
				billMem*(duty*p.ContainerApps.MemoryGiBSecondActive+(1-duty)*p.ContainerApps.MemoryGiBSecondIdle) +
				max0(p.Assumptions.ContainerAppsRequests-p.ContainerApps.FreeGrant.Requests)/1e6*p.ContainerApps.RequestsPerMillion
			l.detail = fmt.Sprintf("%g vCPU x %g GiB x %g replica(s), %.0f%% duty", cpu, mem, replicas, duty*100)

		case "azurerm_container_app_job":
			// A job that runs for a few minutes a day sits inside the free
			// grant the always-on app has already mostly consumed. Called out
			// as negligible rather than omitted, so the reader knows it was
			// considered.
			l.detail = "runs briefly on a schedule; inside the consumption free grant"
			l.monthly = 0

		case "azurerm_storage_account":
			gb := p.Assumptions.GoldenStorageGB
			rate := p.Storage.HotLRSPerGBMonth
			if r, _ := after["account_replication_type"].(string); strings.EqualFold(r, "GRS") {
				rate = p.Storage.HotGRSPerGBMonth
			}
			l.monthly = gb * rate
			l.detail = fmt.Sprintf("assumes %.0f GB stored", gb)

		case "azurerm_key_vault":
			l.monthly = p.Assumptions.KeyVaultOpsPerMonth / 10000 * p.KeyVault.OperationsPer10k
			l.detail = fmt.Sprintf("assumes %.0f operations", p.Assumptions.KeyVaultOpsPerMonth)

		case "azurerm_log_analytics_workspace":
			gb := max0(p.Assumptions.LogAnalyticsGBPerMonth - p.LogAnalytics.FreeIngestionGB)
			l.monthly = gb*p.LogAnalytics.IngestionPerGB + p.Assumptions.LogAnalyticsGBPerMonth*p.LogAnalytics.RetentionPerGBMon
			l.detail = fmt.Sprintf("assumes %.0f GB/month, %.0f GB free", p.Assumptions.LogAnalyticsGBPerMonth, p.LogAnalytics.FreeIngestionGB)

		case "azurerm_private_dns_zone":
			l.monthly = 0.50
			l.detail = "one hosted zone"

		case "azurerm_monitor_metric_alert":
			// Per rule per month. Every rule this project writes filters to a
			// single time series, which is the unit Azure bills, so one rule is
			// one charge. A rule that split on a dimension with `*` would cost
			// this for each series it produced.
			if p.AzureMonitor.MetricAlertPerMonth == 0 {
				l.unknown, l.detail = true, "no azure_monitor.metric_alert_per_month in pricing.yaml"
				break
			}
			l.monthly = p.AzureMonitor.MetricAlertPerMonth
			l.detail = "one metric alert rule"

		case "azurerm_application_insights_standard_web_test":
			// Billed per execution, and an execution is one LOCATION running
			// once. This is the line people are surprised by: the same test
			// from five locations every five minutes costs five times what it
			// does from one.
			if p.AzureMonitor.WebTestExecution == 0 {
				l.unknown, l.detail = true, "no azure_monitor.web_test_execution in pricing.yaml"
				break
			}
			locations := 1
			if ls, ok := after["geo_locations"].([]any); ok && len(ls) > 0 {
				locations = len(ls)
			}
			every := num(after["frequency"])
			if every == 0 {
				every = 300 // the provider's default, and Azure's minimum
			}
			runs := seconds / every * float64(locations)
			l.monthly = runs * p.AzureMonitor.WebTestExecution
			l.detail = fmt.Sprintf("%d location(s) every %.0fs, %.0f executions", locations, every, runs)

		default:
			if why, ok := free[rc.Type]; ok {
				l.monthly, l.detail = 0, why
				if l.detail == "" {
					l.detail = "no charge"
				}
			} else {
				l.unknown = true
				l.detail = fmt.Sprintf("%s is not in pricing.yaml", rc.Type)
			}
		}

		if l.unknown {
			unknowns++
		}
		total += l.monthly
		lines = append(lines, l)
	}
	return lines, total, unknowns
}

func containerShape(after map[string]any) (cpu, mem, replicas float64) {
	tmpls, _ := after["template"].([]any)
	if len(tmpls) == 0 {
		return 0, 0, 0
	}
	t, _ := tmpls[0].(map[string]any)
	replicas = num(t["min_replicas"])
	cs, _ := t["container"].([]any)
	for _, c := range cs {
		cm, _ := c.(map[string]any)
		cpu += num(cm["cpu"])
		mem += gib(cm["memory"])
	}
	return cpu, mem, replicas
}

// gib reads "1Gi" / "0.5Gi" / "512Mi" as GiB.
func gib(v any) float64 {
	s, _ := v.(string)
	s = strings.TrimSpace(s)
	switch {
	case strings.HasSuffix(s, "Gi"):
		return parse(strings.TrimSuffix(s, "Gi"))
	case strings.HasSuffix(s, "Mi"):
		return parse(strings.TrimSuffix(s, "Mi")) / 1024
	}
	return parse(s)
}

func parse(s string) float64 {
	var f float64
	_, _ = fmt.Sscanf(s, "%g", &f)
	return f
}

func num(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	case string:
		return parse(t)
	}
	return 0
}

func max0(f float64) float64 {
	if f < 0 {
		return 0
	}
	return f
}

func creates(actions []string) bool {
	for _, a := range actions {
		if a == "create" || a == "update" || a == "no-op" {
			return true
		}
	}
	return false
}
