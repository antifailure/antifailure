package main

import "testing"

// No trailing slash, on either spelling. The host sets "trailingSlash":
// "never", so the slashed form is a 301, and this function writes the "More"
// link under all 131 error codes.
//
// The fragment cases stay, because they are the older defect and it is still
// possible to reintroduce it: treating the whole field as a path built
// /docs/reference/cli#af-init/, whose fragment is "af-init/" and matches no
// heading. The link resolved, landed at the top of a 900 line page, and looked
// like it worked. lychee found that one against the built site.
func TestDocsURLWritesTheAddressTheHostServes(t *testing.T) {
	cases := map[string]string{
		"reference/cli":              "/docs/reference/cli",
		"reference/cli#af-init":      "/docs/reference/cli#af-init",
		"concepts/egress#inspection": "/docs/concepts/egress#inspection",
	}
	for in, want := range cases {
		if got := docsURL(in); got != want {
			t.Errorf("docsURL(%q) = %q, want %q", in, got, want)
		}
	}
}
