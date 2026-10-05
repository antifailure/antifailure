package main

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

const stream = `{"Action":"run","Package":"p/a","Test":"TestOne"}
{"Action":"output","Package":"p/a","Test":"TestOne","Output":"=== RUN   TestOne\n"}
{"Action":"pass","Package":"p/a","Test":"TestOne"}
{"Action":"run","Package":"p/a","Test":"TestDocker"}
{"Action":"output","Package":"p/a","Test":"TestDocker","Output":"    docker_test.go:12: skipped: AF_SKIP_DOCKER is set\n"}
{"Action":"skip","Package":"p/a","Test":"TestDocker"}
{"Action":"run","Package":"p/a","Test":"TestBroken"}
{"Action":"run","Package":"p/a","Test":"TestBroken/sub"}
{"Action":"output","Package":"p/a","Test":"TestBroken/sub","Output":"    x_test.go:9: boom\n"}
{"Action":"fail","Package":"p/a","Test":"TestBroken/sub"}
{"Action":"fail","Package":"p/a","Test":"TestBroken"}
{"Action":"fail","Package":"p/a"}
{"Action":"output","Package":"p/b","Output":"# p/b\nb.go:3: undefined: x\n"}
{"Action":"fail","Package":"p/b"}
{"Action":"skip","Package":"p/c"}
not json at all
`

func TestSummariseCountsWhatRanAndWhatDidNot(t *testing.T) {
	res, err := summarise(strings.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	if res.passed != 1 || res.skipped != 1 {
		t.Fatalf("passed %d skipped %d, want 1 and 1", res.passed, res.skipped)
	}
	if len(res.failed) != 1 || res.failed[0] != "p/a TestBroken" {
		t.Fatalf("failed %v, want only the parent test", res.failed)
	}
	// p/a failed because a test failed, which is already counted. p/b failed
	// with no test at all, which is the build failure that takes every test in
	// it along and would otherwise vanish from the count.
	if len(res.brokenPackages) != 1 || res.brokenPackages[0] != "p/b" {
		t.Fatalf("broken packages %v, want p/b", res.brokenPackages)
	}
	if res.skipReasons["skipped: AF_SKIP_DOCKER is set"] != 1 {
		t.Fatalf("skip reasons %v", res.skipReasons)
	}
}

func TestVerdictRefusesEveryWayARunCanBeHollow(t *testing.T) {
	green := result{passed: 10, skipReasons: map[string]int{}}
	if _, ok := verdict(green, 10); !ok {
		t.Fatal("a run at the floor with nothing failing is green")
	}
	if _, ok := verdict(green, 11); ok {
		t.Fatal("a run below the floor was called green")
	}
	failed := result{passed: 10, failed: []string{"p TestX"}, failureOutput: map[string]string{}}
	if _, ok := verdict(failed, 1); ok {
		t.Fatal("a failing test was called green")
	}
	broken := result{passed: 10, brokenPackages: []string{"p"}, failureOutput: map[string]string{}}
	if report, ok := verdict(broken, 1); ok || !strings.Contains(report, "BROKEN PACKAGE p") {
		t.Fatalf("a package that never ran was called green: %s", report)
	}
}

func TestDockerIsDecidedByTheDaemonNotThePlatform(t *testing.T) {
	linux := decideDocker("linux", nil)
	if !contains(linux.env, "AF_REQUIRE_DOCKER=1") || containsPrefix(linux.env, "DOCKER_HOST=") {
		t.Fatalf("a Linux daemon must be used and required: %v", linux.env)
	}
	windows := decideDocker("windows", nil)
	if !contains(windows.env, "AF_SKIP_DOCKER=1") || !containsPrefix(windows.env, "DOCKER_HOST=npipe:") {
		t.Fatalf("a Windows containers daemon must be hidden and the skip acknowledged: %v", windows.env)
	}
	none := decideDocker("", errors.New("not running"))
	if !contains(none.env, "AF_SKIP_DOCKER=1") || containsPrefix(none.env, "DOCKER_HOST=") {
		t.Fatalf("no daemon: %v", none.env)
	}
	for _, m := range []dockerMode{linux, windows, none} {
		if m.reason == "" {
			t.Fatal("every decision is printed with its reason")
		}
	}
}

func TestThePendingNamesAreOneAnchoredPattern(t *testing.T) {
	re := regexp.MustCompile(skipPattern())
	for names := range pendingTests {
		for _, n := range strings.Split(names, "|") {
			if !re.MatchString(n) {
				t.Fatalf("%s is listed and not skipped", n)
			}
			if re.MatchString(n + "Extra") {
				t.Fatalf("the pattern for %s also skips a longer name", n)
			}
		}
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func containsPrefix(xs []string, prefix string) bool {
	for _, x := range xs {
		if strings.HasPrefix(x, prefix) {
			return true
		}
	}
	return false
}
