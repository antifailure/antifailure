package main

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func TestEveryPackageRunsExactlyOnceAndStorageRunsAlone(t *testing.T) {
	var commands [][]string
	err := run("repo", func(dir string, args ...string) (string, error) {
		if dir != filepath.Join("repo", "engine") {
			t.Fatalf("wrong directory: %s", dir)
		}
		commands = append(commands, args)
		if args[0] == "list" {
			return "one\n" + storage + "\ntwo\n", nil
		}
		return "", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"list", "./..."}, {"test", "one", "two", "-race", "-count=1", "-timeout=30m"}, {"test", storage, "-race", "-count=1", "-timeout=30m"}}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %#v, want %#v", commands, want)
	}
}

func TestIncompleteOrDuplicateInventoriesAreRefused(t *testing.T) {
	for _, listing := range []string{"", "one", storage, storage + "\none\none"} {
		calls := 0
		err := run("repo", func(_ string, _ ...string) (string, error) { calls++; return listing, nil })
		if err == nil || calls != 1 {
			t.Fatalf("inventory %q was accepted or tested: err=%v calls=%d", listing, err, calls)
		}
	}
}

func TestACommandFailureCannotBecomeAPass(t *testing.T) {
	for failure := 1; failure <= 3; failure++ {
		calls := 0
		want := errors.New("command failed")
		err := run("repo", func(_ string, _ ...string) (string, error) {
			calls++
			if calls == failure {
				return "", want
			}
			return "one\n" + storage, nil
		})
		if !errors.Is(err, want) || calls != failure {
			t.Fatalf("failure %d was lost: err=%v calls=%d", failure, err, calls)
		}
	}
}
