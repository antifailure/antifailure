package main

import (
	"strings"
	"testing"
)

func sampleImages() imageNotices {
	return imageNotices{
		community: []npmPackage{
			{Name: "hono", Version: "4.0.0", Licence: "MIT"},
			{Name: "drizzle-orm", Version: "0.1.0", Licence: "Apache-2.0", Declared: true,
				Notices: []shipped{{file: "NOTICE", text: "drizzle\nCopyright 2024\n"}}},
		},
		console: []npmPackage{
			{Name: "next/dist/compiled/react", Version: "vendored in next 16.3.4", Licence: "MIT"},
		},
		enterprise: []npmPackage{
			{Name: "jose", Version: "5.0.0", Licence: "MIT"},
		},
	}
}

func TestEachArtifactHasItsOwnMarkedSectionInOrder(t *testing.T) {
	out := render([]target{{os: "linux", arch: "amd64"}}, nil) + renderImages(sampleImages())
	var at []int
	for _, h := range []string{
		"\n## The af binary\n",
		"\n### Go modules (0)\n",
		"\n## The community control plane image\n",
		"\n### npm packages (2)\n",
		"\n### The console export (1)\n",
		"\n## The enterprise control plane image, in addition\n",
		"\n### npm packages (1)\n",
	} {
		i := strings.Index(out, h)
		if i < 0 {
			t.Fatalf("the notices have no %q section:\n%s", strings.TrimSpace(h), out)
		}
		at = append(at, i)
		out = out[:i] + strings.Repeat(" ", len(h)) + out[i+len(h):]
	}
	for i := 1; i < len(at); i++ {
		if at[i] < at[i-1] {
			t.Errorf("section %d comes before section %d", i, i-1)
		}
	}
}

func TestADeclaredLicenceSaysItWasDeclaredAndARecognisedOneDoesNot(t *testing.T) {
	out := renderImages(sampleImages())
	if !strings.Contains(out, "- `drizzle-orm` 0.1.0, Apache-2.0 (declared, no licence file shipped)\n") {
		t.Errorf("a licence taken from a declaration does not say so:\n%s", out)
	}
	if !strings.Contains(out, "- `hono` 4.0.0, MIT\n") {
		t.Errorf("a licence read from its text is marked as declared or missing:\n%s", out)
	}
}

func TestAPackageNoticeIsCarriedInTheImageSection(t *testing.T) {
	out := renderImages(sampleImages())
	if !strings.Contains(out, "#### `drizzle-orm` NOTICE\n\n```text\ndrizzle\nCopyright 2024\n```\n") {
		t.Errorf("the package's NOTICE is not carried verbatim:\n%s", out)
	}
}

func TestTheEnterpriseSectionSaysWhyItHasNoGoModules(t *testing.T) {
	out := renderImages(sampleImages())
	for _, want := range []string{"tools/release/build.sh builds only engine/cmd/af", "excludes ee/engine",
		"no enterprise Go modules"} {
		if !strings.Contains(strings.Join(strings.Fields(out), " "), want) {
			t.Errorf("the enterprise section does not say %q:\n%s", want, out)
		}
	}
}

func TestTheEnterpriseAdditionsAreOnlyWhatTheCommunityImageLacks(t *testing.T) {
	community := []npmPackage{{Name: "hono", Version: "4.0.0"}, {Name: "zod", Version: "3.0.0"}}
	enterprise := []npmPackage{
		{Name: "hono", Version: "4.0.0"},
		{Name: "zod", Version: "3.1.0"},
		{Name: "jose", Version: "5.0.0"},
		{Name: "jose", Version: "5.0.0"},
	}
	var got []string
	for _, p := range additions(enterprise, community) {
		got = append(got, p.Name+"@"+p.Version)
	}
	if want := "jose@5.0.0 zod@3.1.0"; strings.Join(got, " ") != want {
		t.Errorf("additions = %v, want %s: a shared package is not an addition, a different version is, and a repeat is listed once", got, want)
	}
}
