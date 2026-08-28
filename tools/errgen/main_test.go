package main

import "testing"

// Two codes in the catalog point at a heading rather than a page. Appending
// the trailing slash to the whole string built /docs/reference/cli#af-init/,
// whose fragment is "af-init/" and matches no heading: the link resolved,
// landed at the top of a 900 line page, and looked like it worked. lychee
// found it against the built site.
func TestDocsURLPutsTheSlashOnThePathNotAfterTheFragment(t *testing.T) {
	cases := map[string]string{
		"reference/cli":              "/docs/reference/cli/",
		"reference/cli#af-init":      "/docs/reference/cli/#af-init",
		"concepts/egress#inspection": "/docs/concepts/egress/#inspection",
	}
	for in, want := range cases {
		if got := docsURL(in); got != want {
			t.Errorf("docsURL(%q) = %q, want %q", in, got, want)
		}
	}
}
