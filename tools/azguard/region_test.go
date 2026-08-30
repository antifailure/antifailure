package main

import "testing"

// The two documents below are the SHAPE Azure actually returned on 2026-08-28,
// trimmed to the fields this cares about. They are fixtures rather than live
// calls so the test runs offline, and they are real rather than invented so the
// decoder is tested against the thing it will meet.
const (
	// eastus. The empty version list is not an editorial simplification: this
	// is exactly what the API returns, and it is what produced
	// "The value of 'Version' should be in: []" from a clean plan.
	eastusRestricted = `[{
	  "name": "FlexibleServerCapabilities",
	  "reason": "Provisioning is restricted in this region. Please choose a different region.",
	  "supportedServerVersions": [],
	  "supportedServerEditions": null
	}]`

	centralusOK = `[{
	  "name": "FlexibleServerCapabilities",
	  "reason": null,
	  "supportedServerVersions": [
	    {"name": "16"}, {"name": "17"}, {"name": "18"}
	  ],
	  "supportedServerEditions": [
	    {"name": "Burstable", "supportedServerSkus": [
	      {"name": "Standard_B1ms"}, {"name": "Standard_B2s"}
	    ]},
	    {"name": "GeneralPurpose", "supportedServerSkus": [
	      {"name": "Standard_D2ds_v4"}
	    ]}
	  ]
	}]`
)

func TestARestrictedRegionIsRefusedAndSaysWhy(t *testing.T) {
	err := postgresRegionOK([]byte(eastusRestricted), "eastus", "17", "Standard_B1ms")
	if err == nil {
		t.Fatal("eastus returns supportedServerVersions: [] and the guard allowed it. This is the exact document that cost an apply twenty six resources.")
	}
	// The message has to carry Azure's own reason. A refusal that says only
	// "not available" sends somebody to try a different SKU, which will also
	// fail, because the restriction is not about the SKU.
	for _, want := range []string{"eastus", "empty", "Provisioning is restricted"} {
		if !contains(err.Error(), want) {
			t.Errorf("refusal does not mention %q: %v", want, err)
		}
	}
}

func TestAGoodRegionIsAllowed(t *testing.T) {
	if err := postgresRegionOK([]byte(centralusOK), "centralus", "17", "Standard_B1ms"); err != nil {
		t.Fatalf("centralus offers 17 and Standard_B1ms and was refused: %v", err)
	}
}

func TestAVersionTheRegionDoesNotOfferIsRefused(t *testing.T) {
	err := postgresRegionOK([]byte(centralusOK), "centralus", "12", "Standard_B1ms")
	if err == nil {
		t.Fatal("centralus does not offer 12 in this document and the guard allowed it")
	}
	if !contains(err.Error(), "16, 17, 18") {
		t.Errorf("refusal should list what IS offered so the reader can pick one: %v", err)
	}
}

func TestASkuTheRegionDoesNotOfferIsRefused(t *testing.T) {
	if err := postgresRegionOK([]byte(centralusOK), "centralus", "17", "Standard_B99ms"); err == nil {
		t.Fatal("Standard_B99ms is in no edition in this document and the guard allowed it")
	}
}

// FAILING CLOSED IS THE WHOLE POINT, so each way of not knowing gets its own
// test. Every one of these is a case where the honest answer is "I could not
// tell", and every one of them must be a refusal rather than a pass.
func TestNotKnowingIsAlwaysARefusal(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		why  string
	}{
		{"unparseable", `{"not":"an array"}`,
			"a response shape we do not understand could be anything"},
		{"empty array", `[]`,
			"no capability document at all is not the same as a region with nothing in it"},
		{"versions but no editions", `[{"supportedServerVersions":[{"name":"17"}],"supportedServerEditions":[]}]`,
			"we cannot confirm the SKU from a document that lists none"},
		{"editions present but empty sku lists", `[{"supportedServerVersions":[{"name":"17"}],"supportedServerEditions":[{"name":"Burstable","supportedServerSkus":[]}]}]`,
			"an edition with no SKUs tells us nothing about the SKU we want"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := postgresRegionOK([]byte(c.raw), "somewhere", "17", "Standard_B1ms"); err == nil {
				t.Fatalf("allowed on input it could not read: %s", c.why)
			}
		})
	}
}

// Terraform spells the SKU with a tier prefix and Azure does not. Getting this
// backwards makes the guard compare "B_Standard_B1ms" against a list that only
// ever contains "Standard_B1ms", so it refuses every region on earth and looks
// like the region is broken.
func TestTerraformSkuNamesAreTranslatedToAzureOnes(t *testing.T) {
	for in, want := range map[string]string{
		"B_Standard_B1ms":     "Standard_B1ms",
		"GP_Standard_D2ds_v4": "Standard_D2ds_v4",
		"MO_Standard_E2ds_v4": "Standard_E2ds_v4",
		"Standard_B1ms":       "Standard_B1ms",
	} {
		if got := terraformSkuToAzure(in); got != want {
			t.Errorf("terraformSkuToAzure(%q) = %q, want %q", in, got, want)
		}
	}
}

// The positive control. Every test above asserts a refusal, and a guard that
// refuses everything passes all of them. This one asserts the SKU translation
// and the capability check work TOGETHER on the input a real caller sends.
func TestTheDefaultTerraformSkuIsAcceptedInAGoodRegion(t *testing.T) {
	if err := postgresRegionOK([]byte(centralusOK), "centralus", "17", terraformSkuToAzure("B_Standard_B1ms")); err != nil {
		t.Fatalf("the stack's own default sku_name was refused by its own guard: %v", err)
	}
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || len(haystack) >= len(needle) && indexOfSubstring(haystack, needle) >= 0
}

func indexOfSubstring(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
