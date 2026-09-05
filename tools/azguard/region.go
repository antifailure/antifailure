package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// A region can refuse a resource for a reason no plan and no policy will tell
// you about, and this is the command that asks.
//
// THE FAILURE THIS EXISTS FOR, in full, because the shape of it is the lesson.
// The control plane was moved from southcentralus to eastus to satisfy a policy
// assignment that denies every other region. `terraform plan` was clean, 27 to
// add. `terraform apply` created twenty six of those twenty seven resources and
// then failed on the database with:
//
//	ParameterOutOfRange: The value of 'Version' should be in: []
//
// The empty list is literal, and it is the whole story. Asking Azure directly:
//
//	az postgres flexible-server list-skus -l eastus
//	  reason: "Provisioning is restricted in this region."
//	  supportedServerVersions: []
//
// PostgreSQL flexible server cannot be created in eastus on this subscription
// at any version in any SKU, while every other resource in the stack creates
// there quite happily. So there are THREE independent gates on a region and
// they are checked by three different systems at three different times:
//
//	quota                 az vm list-usage. Checked first by everybody, and it
//	                      was never the constraint here: 65 cores in both.
//	Azure Policy          evaluated by Azure at WRITE time. A plan cannot see
//	                      it, and a deny assignment refuses a clean plan.
//	regional availability  a property of the subscription, invisible to both
//	                      the plan AND the policy, and only discoverable by
//	                      asking the provider's capabilities endpoint.
//
// This asks the third question before an apply spends fifteen minutes finding
// out. Like the rest of azguard it FAILS CLOSED: if it cannot get an answer it
// refuses, because "I could not tell" and "it is fine" must never look alike.

// pgCapabilities is the part of the flexible server capability document this
// cares about. Deliberately a partial decode: Azure adds fields to this
// response and a strict decoder would turn a new field into an outage.
type pgCapabilities struct {
	Reason string `json:"reason"`
	// A to-MANY relation, so an array. Empty is the restricted case and is the
	// single most important value in this file: it is what eastus returns.
	SupportedServerVersions []struct {
		Name string `json:"name"`
	} `json:"supportedServerVersions"`
	SupportedServerEditions []struct {
		Name                string `json:"name"`
		SupportedServerSkus []struct {
			Name string `json:"name"`
		} `json:"supportedServerSkus"`
	} `json:"supportedServerEditions"`
}

// postgresRegionOK is the whole decision, and it is pure so that it can be
// tested against a captured response rather than against Azure's mood.
//
// sku is the AZURE name (Standard_B1ms), not Terraform's tier-prefixed spelling
// (B_Standard_B1ms). The caller converts, because the conversion is a property
// of Terraform rather than of Azure and this function should not know about
// Terraform at all.
func postgresRegionOK(raw []byte, location, version, sku string) error {
	var docs []pgCapabilities
	if err := json.Unmarshal(raw, &docs); err != nil {
		return fmt.Errorf("could not parse the capabilities for %s: %w", location, err)
	}
	if len(docs) == 0 {
		return fmt.Errorf("azure returned no capability document for %s at all, which is not the same as a region with nothing available, so this refuses rather than guesses", location)
	}
	c := docs[0]

	if len(c.SupportedServerVersions) == 0 {
		reason := strings.TrimSpace(c.Reason)
		if reason == "" {
			reason = "azure gave no reason"
		}
		return fmt.Errorf("PostgreSQL flexible server cannot be created in %s: supportedServerVersions is empty. Azure says: %s. An apply here fails with ParameterOutOfRange and the message \"The value of 'Version' should be in: []\", AFTER creating every other resource in the stack",
			location, reason)
	}

	if version != "" {
		var have []string
		found := false
		for _, v := range c.SupportedServerVersions {
			have = append(have, v.Name)
			if v.Name == version {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("%s offers PostgreSQL versions %s, and this stack asks for %s", location, strings.Join(have, ", "), version)
		}
	}

	if sku != "" {
		var haveAnySku bool
		for _, ed := range c.SupportedServerEditions {
			for _, s := range ed.SupportedServerSkus {
				haveAnySku = true
				if strings.EqualFold(s.Name, sku) {
					return nil
				}
			}
		}
		if !haveAnySku {
			// Versions present but no SKU list at all. Refuse rather than pass:
			// a document we cannot read the SKUs out of is one we cannot make
			// this claim from.
			return fmt.Errorf("%s lists PostgreSQL versions but no server SKUs, so this cannot confirm %s is available and refuses rather than assuming", location, sku)
		}
		return fmt.Errorf("%s does not offer the PostgreSQL SKU %s", location, sku)
	}
	return nil
}

// terraformSkuToAzure converts Terraform's sku_name to the name Azure's
// capabilities endpoint uses. Terraform spells it <TIER>_<NAME>, so
// B_Standard_B1ms is tier B and SKU Standard_B1ms, and the tier letter is not
// part of the SKU name anywhere in Azure's own API.
func terraformSkuToAzure(sku string) string {
	for _, tier := range []string{"B_", "GP_", "MO_"} {
		if strings.HasPrefix(sku, tier) {
			return strings.TrimPrefix(sku, tier)
		}
	}
	return sku
}

func regionCapabilities(location string) ([]byte, error) {
	out, err := exec.Command("az", "postgres", "flexible-server", "list-skus", "-l", location, "-o", "json").Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, fmt.Errorf("could not read the PostgreSQL capabilities for %s: %s", location, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("could not run az to read the PostgreSQL capabilities for %s: %w", location, err)
	}
	return out, nil
}

func cmdRegion(args []string) int {
	version := "17"
	sku := "B_Standard_B1ms"
	var locations []string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--postgres-version":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "azguard region: --postgres-version needs a value")
				return 2
			}
			i++
			version = args[i]
		case "--postgres-sku":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "azguard region: --postgres-sku needs a value")
				return 2
			}
			i++
			sku = args[i]
		default:
			if strings.HasPrefix(args[i], "-") {
				fmt.Fprintf(os.Stderr, "azguard region: unknown flag %s\n", args[i])
				return 2
			}
			locations = append(locations, args[i])
		}
	}
	if len(locations) == 0 {
		fmt.Fprintln(os.Stderr, "azguard region: no location given")
		return 2
	}

	azureSku := terraformSkuToAzure(sku)
	var errs []error
	for _, loc := range locations {
		raw, err := regionCapabilities(loc)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := postgresRegionOK(raw, loc, version, azureSku); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		report(errs)
		return 1
	}
	_, _ = fmt.Fprintf(os.Stdout, "azguard: %s can create PostgreSQL %s on %s\n", strings.Join(locations, ", "), version, azureSku)
	return 0
}
