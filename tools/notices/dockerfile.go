package main

// THE FAILURE THIS FILE IS FOR. A control plane image ships node_modules, and
// which packages are in it is decided by one `npm ci` line in a Dockerfile, not
// by the lockfile. Reading the lockfile instead attributes the wrong thing in
// both directions: it lists packages a scoped install leaves out, and its
// declared licence field is not the licence text the package carries, which is
// how posthog-node reads as MIT while its LICENSE file is Apache 2.0 and MIT.
//
// So the notices come from the install the image actually runs. The install
// stage is read out of the Dockerfile and replayed: the same files copied into
// the same layout, the same `npm ci` with the same flags, in a temporary
// directory. That is the same choice this tool makes about platforms, which it
// reads from the release workflow rather than keeping a second list: a flag
// changed in a Dockerfile changes what is attributed, and nobody has to remember
// that this file exists.

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// step is one instruction of an install stage that replaying it needs.
type step struct {
	kind string   // "workdir", "copy" or "npm"
	args []string // the path, the sources then the destination, or npm ci's arguments
}

// stage is the replayable part of one named Dockerfile stage.
type stage struct {
	file  string
	name  string
	steps []step
}

var (
	fromLine = regexp.MustCompile(`(?i)^FROM\s+\S+(?:\s+AS\s+(\S+))?\s*$`)
	npmCI    = regexp.MustCompile(`\bnpm\s+ci\b([^&;|]*)`)
)

// instructions reads a Dockerfile into whole instructions: continuation lines
// joined, and comment lines dropped wherever they sit, including inside a
// continued instruction, which Docker allows and these files do.
func instructions(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var out []string
	var current strings.Builder
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<16), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "#") || (line == "" && current.Len() == 0) {
			continue
		}
		if strings.HasSuffix(line, `\`) {
			current.WriteString(strings.TrimSuffix(line, `\`) + " ")
			continue
		}
		current.WriteString(line)
		if s := strings.TrimSpace(current.String()); s != "" {
			out = append(out, s)
		}
		current.Reset()
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if current.Len() > 0 {
		out = append(out, strings.TrimSpace(current.String()))
	}
	return out, nil
}

// parseStage returns the WORKDIR, COPY and `npm ci` instructions of one named
// stage, in order.
//
// It refuses rather than guesses. A stage that is not in the file, a stage with
// no `npm ci`, and a COPY from another stage, which would bring in content this
// cannot reproduce from the repository, are each a failure naming the file and
// the stage.
func parseStage(path, name string) (stage, error) {
	lines, err := instructions(path)
	if err != nil {
		return stage{}, err
	}
	st := stage{file: path, name: name}
	in, found := false, false
	for _, ins := range lines {
		if m := fromLine.FindStringSubmatch(ins); m != nil {
			in = strings.EqualFold(m[1], name)
			found = found || in
			continue
		}
		if !in {
			continue
		}
		fields := strings.Fields(ins)
		switch strings.ToUpper(fields[0]) {
		case "WORKDIR":
			if len(fields) != 2 {
				return stage{}, fmt.Errorf("%s stage %s: WORKDIR takes one path: %q", path, name, ins)
			}
			st.steps = append(st.steps, step{kind: "workdir", args: fields[1:]})
		case "COPY":
			args := fields[1:]
			for len(args) > 0 && strings.HasPrefix(args[0], "--") {
				if strings.HasPrefix(args[0], "--from") {
					return stage{}, fmt.Errorf("%s stage %s copies from another stage (%q), which cannot be "+
						"replayed from the repository", path, name, ins)
				}
				args = args[1:]
			}
			if len(args) < 2 {
				return stage{}, fmt.Errorf("%s stage %s: COPY needs a source and a destination: %q", path, name, ins)
			}
			st.steps = append(st.steps, step{kind: "copy", args: args})
		case "RUN":
			if m := npmCI.FindStringSubmatch(ins); m != nil {
				st.steps = append(st.steps, step{kind: "npm", args: strings.Fields(m[1])})
			}
		}
	}
	if !found {
		return stage{}, fmt.Errorf("%s has no stage called %s, so what it installs cannot be read", path, name)
	}
	npm := 0
	for _, s := range st.steps {
		if s.kind == "npm" {
			npm++
		}
	}
	if npm != 1 {
		return stage{}, fmt.Errorf("%s stage %s runs npm ci %d times, and a notice is generated from exactly "+
			"one install; if the stage changed shape, this has to be taught the new one", path, name, npm)
	}
	return st, nil
}

// replay lays out what the stage copies into tmp, as the image lays it out, and
// runs the stage's own `npm ci` there. It returns the directory the install ran
// in, which is where node_modules is.
//
// npm is the binary to run, passed in so a test can supply one that records what
// it was asked to do without touching the network. --prefer-offline is added so
// that a warm npm cache needs no network at all; the image's own flags are kept
// exactly, because they are what decides which packages ship.
func replay(root string, st stage, tmp, npm string, tracked func(dir string) ([]string, error)) (string, error) {
	cwd := tmp
	installDir := ""
	for _, s := range st.steps {
		switch s.kind {
		case "workdir":
			if filepath.IsAbs(s.args[0]) {
				cwd = filepath.Join(tmp, s.args[0])
			} else {
				cwd = filepath.Join(cwd, s.args[0])
			}
			if err := os.MkdirAll(cwd, 0o755); err != nil {
				return "", err
			}
		case "copy":
			if err := copyStep(root, cwd, tmp, s.args, tracked); err != nil {
				return "", fmt.Errorf("%s stage %s: %w", st.file, st.name, err)
			}
		case "npm":
			args := append([]string{"ci"}, s.args...)
			args = append(args, "--prefer-offline", "--no-audit", "--no-fund")
			cmd := exec.Command(npm, args...)
			cmd.Dir = cwd
			cmd.Env = append(os.Environ(), "npm_config_update_notifier=false")
			out, err := cmd.CombinedOutput()
			// A bare name that is not on the path is exec.ErrNotFound; a path to a
			// binary that is not there is a missing file. Both mean no npm.
			if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
				return "", fmt.Errorf("npm is not on the path. The image notices are generated from the "+
					"install %s stage %s actually runs, so this needs Node and npm; it does not fall back "+
					"to reading the lockfile, because that attributes a different set of packages", st.file, st.name)
			}
			if err != nil {
				return "", fmt.Errorf("npm ci for %s stage %s failed: %w\n%s\nWith a warm npm cache this "+
					"needs no network. Offline with a cold cache, it cannot fetch the packages, and the "+
					"notices are not generated rather than generated from something else",
					st.file, st.name, err, tail(string(out), 12))
			}
			installDir = cwd
		}
	}
	return installDir, nil
}

// copyStep reproduces one COPY. A source that is a directory contributes its
// tracked files, which is what a clean checkout's build context holds; build
// output and node_modules are what the image's own ignore file excludes, and a
// working tree's copies of them are not something the image was built from.
func copyStep(root, cwd, tmp string, args []string, tracked func(dir string) ([]string, error)) error {
	sources, dest := args[:len(args)-1], args[len(args)-1]
	var target string
	if filepath.IsAbs(dest) {
		target = filepath.Join(tmp, dest)
	} else {
		target = filepath.Join(cwd, dest)
	}
	intoDir := strings.HasSuffix(dest, "/") || dest == "." || len(sources) > 1
	for _, src := range sources {
		from := filepath.Join(root, filepath.FromSlash(strings.TrimSuffix(src, "/")))
		info, err := os.Stat(from)
		if err != nil {
			return fmt.Errorf("COPY %s: %w", src, err)
		}
		if info.IsDir() {
			files, err := tracked(from)
			if err != nil {
				return fmt.Errorf("COPY %s: listing its tracked files: %w", src, err)
			}
			for _, rel := range files {
				if err := copyFile(filepath.Join(from, rel), filepath.Join(target, rel)); err != nil {
					return err
				}
			}
			continue
		}
		to := target
		if intoDir {
			to = filepath.Join(target, filepath.Base(from))
		}
		if err := copyFile(from, to); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(from, to string) error {
	body, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	return os.WriteFile(to, body, 0o644)
}

// gitTracked lists the tracked files under a directory, relative to it.
func gitTracked(dir string) ([]string, error) {
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files in %s: %w", dir, err)
	}
	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" {
			files = append(files, filepath.FromSlash(f))
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("git ls-files in %s listed nothing, so a COPY of it would copy nothing", dir)
	}
	return files, nil
}

// tail keeps the end of a long output, which is where npm says what went wrong.
func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
