// Command varcheck proves that every environment variable the product NAMES AT
// A USER is one the documentation explains.
//
// The failure that earned it: `af license install` prints "Set AF_LICENSE_KEY
// and AF_ORG where the engine runs", then points the reader at
// antifailure.dev/docs/enterprise/licensing, and that page named neither
// variable. So the product asked for two things, sent the reader to the one
// page that should have said what they are, and that page could not answer.
// The correct instructions did not exist anywhere. `af doctor` had the same
// shape: it recommends AF_PORT_RANGE_START to somebody whose ports are busy,
// and nothing documented it.
//
// The control plane's variables have had this gate since somebody wrote
// web/apps/api/test/config-docs.test.ts. The engine's never did, and the engine
// is the half a customer runs on their own machine.
//
// WHAT COUNTS AS NAMING IT AT A USER, and why this reads an AST rather than
// grepping. The first version of this check was a line-oriented grep and it
// returned a clean zero over AF_PORT_RANGE_START while looking straight at it,
// because `r.Remediation = fmt.Sprintf(` and the string that names the variable
// are on different lines. That is the wrapped-statement trap this repository's
// contract opens with, and a pattern that cannot match looks exactly like a
// pattern that found nothing. Parsing removes the question: a string literal is
// an argument to a call or it is not, however the source is wrapped.
//
// Four shapes, and each one is somewhere a person reads the name:
//
//   - an argument to Print, Printf, Println, Fprint, Fprintf or Fprintln
//   - a Short, Long, Remediation, Example or Next field
//   - the usage string of a cobra flag registration
//   - anything in the error catalogue, every string of which is printed
//
// THE EXEMPTION FILE IS THE IMPORTANT HALF, and it is the same mechanism
// tools/docs/figure-exemptions.tsv uses. A variable may be exempted by a row
// that STATES A REASON, because an exemption with no argument behind it cannot
// be told apart from somebody silencing a finding they did not understand. A
// row that stops being needed is reported, so the file cannot rot into a
// permanent allowance the way a hand-maintained list does.
//
// It is empty today. That is worth saying rather than treating as an accident:
// every variable this product names at a user is currently documented, so the
// file exists for the next one rather than for a backlog.
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	varName = regexp.MustCompile(`\bAF_[A-Z0-9_]+`)
	// Printing calls, by the selector's final name.
	printers = map[string]bool{
		"Print": true, "Printf": true, "Println": true,
		"Fprint": true, "Fprintf": true, "Fprintln": true,
	}
	// Struct fields whose value a person reads.
	spokenFields = map[string]bool{
		"Short": true, "Long": true, "Remediation": true, "Example": true, "Next": true,
	}
	// Cobra flag registrations. The usage string is the last argument and it is
	// printed by --help, which is where somebody looks first.
	flagFuncs = regexp.MustCompile(`^(String|Bool|Int|Int64|Float64|Duration|StringSlice|StringArray|Count)(Var|VarP)?(P)?$`)
)

type finding struct{ name, where string }

func main() {
	exemptOnly := flag.Bool("exemptions", false, "print the exemption file's effective rows and exit")
	list := flag.Bool("list", false, "print every variable named at a user and where, then exit")
	flag.Parse()
	root := "."
	if flag.NArg() > 0 {
		root = flag.Arg(0)
	}

	spoken, err := spokenVariables(root)
	if err != nil {
		fail(err)
	}
	documented, err := documentedVariables(root)
	if err != nil {
		fail(err)
	}
	exempt, order, err := exemptions(root)
	if err != nil {
		fail(err)
	}
	if *list {
		ns := make([]string, 0, len(spoken))
		for n, w := range spoken {
			ns = append(ns, n+"\t"+w)
		}
		sort.Strings(ns)
		for _, n := range ns {
			fmt.Println(n)
		}
		return
	}
	if *exemptOnly {
		for _, name := range order {
			fmt.Printf("%s\t%s\n", name, exempt[name])
		}
		return
	}

	var undocumented []finding
	names := make([]string, 0, len(spoken))
	for n := range spoken {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if documented[n] || exempt[n] != "" {
			continue
		}
		undocumented = append(undocumented, finding{n, spoken[n]})
	}

	// A row that is no longer needed. Same half figurecheck has, and the reason
	// it has it: cli-onboarding found five stale rows in the figure list
	// tonight, which is only possible because staleness is reported.
	var stale []string
	for _, n := range order {
		switch {
		case spoken[n] == "":
			stale = append(stale, fmt.Sprintf("%s is exempt and nothing names it at a user any more", n))
		case documented[n]:
			stale = append(stale, fmt.Sprintf("%s is exempt and is now documented, so the row can go", n))
		}
	}

	if len(undocumented) == 0 && len(stale) == 0 {
		fmt.Printf("varcheck: %d variables named at a user, every one documented, %d exemptions\n",
			len(spoken), len(order))
		return
	}

	for _, f := range undocumented {
		fmt.Fprintf(os.Stderr, "  %s is named at %s and no published page mentions it\n", f.name, f.where)
	}
	for _, s := range stale {
		fmt.Fprintf(os.Stderr, "  %s\n", s)
	}
	fmt.Fprintf(os.Stderr, "\nvarcheck: %d named at a user with no page, %d stale exemptions.\n",
		len(undocumented), len(stale))
	fmt.Fprintf(os.Stderr, "Document it under docs/src/content/docs, or add a row to %s with the reason.\n",
		exemptionsPath)
	os.Exit(1)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "varcheck:", err)
	os.Exit(1)
}

const exemptionsPath = "tools/docs/variable-exemptions.tsv"

// spokenVariables maps each variable to the first place a user is shown it.
func spokenVariables(root string) (map[string]string, error) {
	out := map[string]string{}

	// The error catalogue, every string of which reaches a terminal.
	catalog := filepath.Join(root, "engine", "internal", "errors", "catalog.yaml")
	if b, err := os.ReadFile(catalog); err == nil {
		for _, m := range varName.FindAllString(string(b), -1) {
			note(out, m, "engine/internal/errors/catalog.yaml")
		}
	}

	fset := token.NewFileSet()
	for _, dir := range []string{"engine", "ee"} {
		base := filepath.Join(root, dir)
		if _, err := os.Stat(base); err != nil {
			continue
		}
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			// Tests are not a user-facing surface, and testdata is fixtures.
			if d.IsDir() {
				if d.Name() == "testdata" || d.Name() == "node_modules" {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				// A file this cannot parse is not a reason to fail the gate,
				// but it IS a reason to say so: silently skipping it would be
				// a gate green over a file it never read.
				fmt.Fprintf(os.Stderr, "varcheck: could not parse %s: %v\n", path, perr)
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			where := filepath.ToSlash(rel)
			ast.Inspect(file, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.CallExpr:
					sel, ok := x.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					name := sel.Sel.Name
					if !printers[name] && !flagFuncs.MatchString(name) {
						return true
					}
					for _, arg := range x.Args {
						for _, v := range literalVars(arg) {
							note(out, v, where)
						}
					}
				case *ast.KeyValueExpr:
					key, ok := x.Key.(*ast.Ident)
					if !ok || !spokenFields[key.Name] {
						return true
					}
					for _, v := range literalVars(x.Value) {
						note(out, v, where)
					}
				case *ast.AssignStmt:
					// r.Remediation = fmt.Sprintf("... AF_X ...", n)
					for i, lhs := range x.Lhs {
						sel, ok := lhs.(*ast.SelectorExpr)
						if !ok || !spokenFields[sel.Sel.Name] || i >= len(x.Rhs) {
							continue
						}
						for _, v := range literalVars(x.Rhs[i]) {
							note(out, v, where)
						}
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// literalVars pulls variable names out of any string literal inside an
// expression, however deeply it is nested in a call or a concatenation. A
// remedy is routinely fmt.Sprintf of three joined pieces.
func literalVars(e ast.Expr) []string {
	var out []string
	ast.Inspect(e, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		s, err := strconv.Unquote(lit.Value)
		if err != nil {
			s = lit.Value
		}
		out = append(out, varName.FindAllString(s, -1)...)
		return true
	})
	return out
}

func note(m map[string]string, name, where string) {
	if _, seen := m[name]; !seen {
		m[name] = where
	}
}

func documentedVariables(root string) (map[string]bool, error) {
	out := map[string]bool{}
	dir := filepath.Join(root, "docs", "src", "content", "docs")
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || (!strings.HasSuffix(path, ".md") && !strings.HasSuffix(path, ".mdx")) {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		for _, m := range varName.FindAllString(string(b), -1) {
			out[m] = true
		}
		return nil
	})
	return out, err
}

// exemptions returns the reason per variable, and the file's order so that a
// stale row is reported where a reader will find it.
func exemptions(root string) (map[string]string, []string, error) {
	path := filepath.Join(root, exemptionsPath)
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]string{}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	reasons := map[string]string{}
	var order []string
	for i, line := range strings.Split(string(b), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
			return nil, nil, fmt.Errorf("%s:%d has no reason. Two tab separated fields, "+
				"the variable and why it is allowed to be undocumented", exemptionsPath, i+1)
		}
		name := strings.TrimSpace(parts[0])
		reasons[name] = strings.TrimSpace(parts[1])
		order = append(order, name)
	}
	return reasons, order, nil
}
