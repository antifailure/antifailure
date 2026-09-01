package reaper

import "strconv"

// parseUnix reads the label's integer seconds.
//
// Its own function so that the label encoding is stated in one place. Unix
// seconds and not RFC 3339, because a Kubernetes label value may not contain a
// colon and every RFC 3339 timestamp does.
func parseUnix(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}
