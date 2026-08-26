package redact_test

import "regexp"

func mustCompile(s string) *regexp.Regexp { return regexp.MustCompile(s) }
