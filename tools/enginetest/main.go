// Command enginetest runs every engine package, with storage measurements
// isolated from the other packages that exercise the same Docker daemon.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const storage = "github.com/antifailure/antifailure/engine/internal/db/docker"

type invoke func(dir string, args ...string) (string, error)

func run(root string, call invoke) error {
	dir := filepath.Join(root, "engine")
	listed, err := call(dir, "list", "./...")
	if err != nil {
		return fmt.Errorf("listing engine packages: %w", err)
	}
	var parallel []string
	seen := map[string]bool{}
	for _, pkg := range strings.Fields(listed) {
		if seen[pkg] {
			return fmt.Errorf("package listed twice: %s", pkg)
		}
		seen[pkg] = true
		if pkg != storage {
			parallel = append(parallel, pkg)
		}
	}
	if !seen[storage] || len(parallel) == 0 {
		return fmt.Errorf("the package inventory must contain the Docker database package and the remaining engine packages")
	}
	args := append([]string{"test"}, parallel...)
	args = append(args, "-race", "-count=1", "-timeout=30m")
	if _, err := call(dir, args...); err != nil {
		return fmt.Errorf("engine package batch: %w", err)
	}
	if _, err := call(dir, "test", storage, "-race", "-count=1", "-timeout=30m"); err != nil {
		return fmt.Errorf("isolated Docker database suite: %w", err)
	}
	fmt.Printf("enginetest: all %d packages passed; Docker database tests ran separately with unchanged thresholds\n", len(seen))
	return nil
}

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	call := func(dir string, args ...string) (string, error) {
		command := exec.Command("go", args...)
		command.Dir = dir
		command.Stderr = os.Stderr
		if args[0] == "list" {
			out, err := command.Output()
			return string(out), err
		}
		command.Stdout = os.Stdout
		return "", command.Run()
	}
	if err := run(root, call); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
