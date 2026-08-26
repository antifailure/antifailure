package build

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// mapFS is a build context described inline, so a buildpack test states
// exactly the files it depends on and nothing else.
type mapFS map[string]string

func (m mapFS) Has(p string) bool { _, ok := m[p]; return ok }
func (m mapFS) Read(p string) ([]byte, bool) {
	v, ok := m[p]
	return []byte(v), ok
}

func detect(t *testing.T, fs mapFS, dir, command string, port int) *Buildpack {
	t.Helper()
	bp, ok := DetectBuildpack(fs, dir, command, port)
	require.True(t, ok, "no buildpack matched")
	return bp
}

func TestBuildpack_NoneMatchesAnEmptyContext(t *testing.T) {
	t.Parallel()
	_, ok := DetectBuildpack(mapFS{"README.md": "# hi"}, "", "", 0)
	require.False(t, ok, "a repository with no build evidence must say so rather than guess")
}

func TestNodeBuildpack_PicksTheManagerFromTheLockfile(t *testing.T) {
	t.Parallel()
	for lock, want := range map[string]string{
		"pnpm-lock.yaml":    "pnpm install --frozen-lockfile",
		"yarn.lock":         "yarn install --frozen-lockfile",
		"bun.lockb":         "bun install --frozen-lockfile",
		"package-lock.json": "npm ci",
	} {
		fs := mapFS{"package.json": `{"scripts":{"start":"node server.js"}}`, lock: "x"}
		bp := detect(t, fs, "", "", 3000)
		require.Equal(t, "node", bp.Name)
		require.Contains(t, bp.Dockerfile, want, "lockfile %s", lock)
		require.Contains(t, bp.Why, lock)
	}
}

func TestNodeBuildpack_PrefersPnpmOverAStrayNpmLock(t *testing.T) {
	t.Parallel()
	// pnpm and yarn repositories often carry a package-lock.json somebody
	// generated once by accident. Installing from it resolves versions
	// production does not have, which is exactly the class of bug a preview
	// environment exists to catch and would instead introduce.
	fs := mapFS{
		"package.json":      `{"scripts":{"start":"node server.js"}}`,
		"pnpm-lock.yaml":    "x",
		"package-lock.json": "x",
	}
	bp := detect(t, fs, "", "", 3000)
	require.Contains(t, bp.Dockerfile, "pnpm install --frozen-lockfile")
	require.NotContains(t, bp.Dockerfile, "npm ci")
}

func TestNodeBuildpack_PackageManagerFieldOverridesTheLockfile(t *testing.T) {
	t.Parallel()
	fs := mapFS{
		"package.json":      `{"packageManager":"yarn@4.1.0","scripts":{"start":"node s.js"}}`,
		"package-lock.json": "x",
	}
	bp := detect(t, fs, "", "", 0)
	require.Contains(t, bp.Dockerfile, "yarn install --frozen-lockfile")
}

func TestNodeBuildpack_SaysSoWhenThereIsNoLockfile(t *testing.T) {
	t.Parallel()
	// Not a warning to be tidy. Without a lockfile the install resolves fresh
	// versions, so the environment is not running the application production
	// runs, and a result from it means less than it appears to.
	bp := detect(t, mapFS{"package.json": `{"scripts":{"start":"node s.js"}}`}, "", "", 0)
	require.Contains(t, bp.Why, "No lockfile")
	require.Contains(t, bp.Why, "versions production does not have")
}

func TestNodeBuildpack_InstallsBeforeCopyingSource(t *testing.T) {
	t.Parallel()
	// The layer that makes a rebuild fast. If COPY . . came first, every
	// source edit would reinstall the dependency graph.
	fs := mapFS{"package.json": `{"scripts":{"start":"node s.js"}}`, "pnpm-lock.yaml": "x"}
	d := detect(t, fs, "", "", 0).Dockerfile
	require.Less(t,
		strings.Index(d, "pnpm install"),
		strings.Index(d, "COPY . ."),
		"the dependency install must not depend on the source")
	require.Contains(t, d, "COPY package.json pnpm-lock.yaml ./")
	require.Contains(t, d, "mkdir -p /app/node_modules",
		"the runtime stage copies it unconditionally, so it has to exist even when "+
			"the application has no dependencies yet")
}

func TestNodeBuildpack_RunsTheBuildScriptWhenThereIsOne(t *testing.T) {
	t.Parallel()
	with := detect(t, mapFS{
		"package.json":   `{"scripts":{"build":"next build","start":"next start"}}`,
		"pnpm-lock.yaml": "x",
	}, "", "", 3000)
	require.Contains(t, with.Dockerfile, "RUN pnpm run build")
	require.Contains(t, with.Why, "build script")

	without := detect(t, mapFS{
		"package.json":   `{"scripts":{"start":"node s.js"}}`,
		"pnpm-lock.yaml": "x",
	}, "", "", 3000)
	require.NotContains(t, without.Dockerfile, "run build")
}

func TestNodeBuildpack_TakesTheVersionFromEnginesThenNvmrc(t *testing.T) {
	t.Parallel()
	engines := detect(t, mapFS{
		"package.json": `{"engines":{"node":">=20.9.0"},"scripts":{"start":"node s.js"}}`,
	}, "", "", 0)
	require.Contains(t, engines.Dockerfile, "FROM node:20-bookworm-slim")

	nvmrc := detect(t, mapFS{
		"package.json": `{"scripts":{"start":"node s.js"}}`,
		".nvmrc":       "v18.19.0\n",
	}, "", "", 0)
	require.Contains(t, nvmrc.Dockerfile, "FROM node:18-bookworm-slim")

	bare := detect(t, mapFS{"package.json": `{}`}, "", "", 0)
	require.Contains(t, bare.Dockerfile, "FROM node:22-bookworm-slim")
}

func TestNodeBuildpack_UsesTheRootLockfileForAMonorepoPackage(t *testing.T) {
	t.Parallel()
	// The package's dependencies are installed from the workspace root. A
	// buildpack that looked only beside package.json would find no lockfile
	// and fall back to a fresh resolve.
	fs := mapFS{
		"pnpm-lock.yaml":            "x",
		"pnpm-workspace.yaml":       "packages:\n  - packages/*\n",
		"packages/web/package.json": `{"scripts":{"start":"next start"}}`,
	}
	bp := detect(t, fs, "packages/web", "", 3000)
	require.Contains(t, bp.Dockerfile, "pnpm install --frozen-lockfile")
	require.Contains(t, bp.Dockerfile, "COPY packages/web/package.json pnpm-lock.yaml pnpm-workspace.yaml ./")
}

func TestNodeBuildpack_DoesNotRunAsRoot(t *testing.T) {
	t.Parallel()
	// A container running as root is a container whose escape is a root
	// escape, and the base image already ships a user for this.
	bp := detect(t, mapFS{"package.json": `{}`}, "", "", 0)
	require.Contains(t, bp.Dockerfile, "USER node")
}

func TestNodeBuildpack_SurvivesAPackageJSONThatDoesNotParse(t *testing.T) {
	t.Parallel()
	// It is still evidence this is a Node service. Falling through to another
	// buildpack would produce an error about the wrong thing entirely.
	bp := detect(t, mapFS{"package.json": `{"scripts": {`}, "", "", 0)
	require.Equal(t, "node", bp.Name)
}

func TestGoBuildpack_BuildsStaticAndShipsNoToolchain(t *testing.T) {
	t.Parallel()
	bp := detect(t, mapFS{"go.mod": "module example.com/x\n\ngo 1.24\n"}, "", "", 8080)
	require.Equal(t, "go", bp.Name)
	require.Contains(t, bp.Dockerfile, "FROM golang:1.24 AS build")
	require.Contains(t, bp.Dockerfile, "CGO_ENABLED=0")
	require.Contains(t, bp.Dockerfile, "gcr.io/distroless/static-debian12:nonroot")
	require.Contains(t, bp.Dockerfile, "USER nonroot")
	require.NotContains(t, bp.Dockerfile, "FROM golang:1.24 AS runtime")
	require.Contains(t, bp.Dockerfile, "EXPOSE 8080")
}

func TestGoBuildpack_DownloadsModulesBeforeCopyingSource(t *testing.T) {
	t.Parallel()
	d := detect(t, mapFS{"go.mod": "module x\n\ngo 1.23\n"}, "", "", 0).Dockerfile
	require.Less(t, strings.Index(d, "go mod download"), strings.Index(d, "COPY . ."))
}

func TestGoBuildpack_BuildsThePackageForTheServiceDirectory(t *testing.T) {
	t.Parallel()
	bp := detect(t, mapFS{"go.mod": "module x\n\ngo 1.23\n"}, "cmd/worker", "", 0)
	require.Contains(t, bp.Dockerfile, "go build -trimpath -o /out/app ./cmd/worker")
}

func TestGoBuildpack_WinsOverNodeWhenBothArePresent(t *testing.T) {
	t.Parallel()
	// A Go service with a small frontend has both. The go.mod is the one that
	// says what the service is.
	bp := detect(t, mapFS{
		"go.mod":       "module x\n\ngo 1.23\n",
		"package.json": `{"scripts":{"build":"vite build"}}`,
	}, "", "", 0)
	require.Equal(t, "go", bp.Name)
}

func TestPythonBuildpack_PicksTheInstallerFromTheLockfile(t *testing.T) {
	t.Parallel()
	for file, want := range map[string]string{
		"uv.lock":          "uv sync --frozen",
		"poetry.lock":      "poetry install",
		"Pipfile.lock":     "pipenv install --deploy",
		"requirements.txt": "pip install --no-cache-dir -r requirements.txt",
	} {
		bp := detect(t, mapFS{file: "x", "pyproject.toml": "[project]\n"}, "", "", 8000)
		require.Equal(t, "python", bp.Name, "file %s", file)
		require.Contains(t, bp.Dockerfile, want, "file %s", file)
	}
}

func TestPythonBuildpack_TakesTheVersionFromPythonVersionThenPyproject(t *testing.T) {
	t.Parallel()
	pinned := detect(t, mapFS{"requirements.txt": "flask\n", ".python-version": "3.11.7\n"}, "", "", 0)
	require.Contains(t, pinned.Dockerfile, "FROM python:3.11-slim")

	fromProject := detect(t, mapFS{
		"pyproject.toml": "[project]\nrequires-python = \">=3.10\"\n",
	}, "", "", 0)
	require.Contains(t, fromProject.Dockerfile, "FROM python:3.10-slim")

	bare := detect(t, mapFS{"requirements.txt": "flask\n"}, "", "", 0)
	require.Contains(t, bare.Dockerfile, "FROM python:3.12-slim")
}

func TestPythonBuildpack_KeepsOutputUnbuffered(t *testing.T) {
	t.Parallel()
	// A crash with a buffered stream loses the traceback that explains it,
	// which is the one thing somebody needs when a preview environment fails.
	bp := detect(t, mapFS{"requirements.txt": "flask\n"}, "", "", 0)
	require.Contains(t, bp.Dockerfile, "PYTHONUNBUFFERED=1")
	require.Contains(t, bp.Dockerfile, "USER app")
}

func TestRubyBuildpack_InstallsInDeploymentMode(t *testing.T) {
	t.Parallel()
	bp := detect(t, mapFS{
		"Gemfile": "source 'https://rubygems.org'\n", ".ruby-version": "3.2.2\n",
	}, "", "bundle exec puma", 3000)
	require.Equal(t, "ruby", bp.Name)
	require.Contains(t, bp.Dockerfile, "FROM ruby:3.2.2-slim")
	require.Contains(t, bp.Dockerfile, "BUNDLE_DEPLOYMENT=1")
	require.Contains(t, bp.Dockerfile, "BUNDLE_WITHOUT=development:test")
	require.Contains(t, bp.Dockerfile, `CMD ["bundle", "exec", "puma"]`)
}

func TestShellForm_UsesExecFormWhenItCan(t *testing.T) {
	t.Parallel()
	// The exec form keeps the process as PID 1, so a stop signal reaches it
	// rather than the shell in front of it, and the container stops in a
	// second instead of after a ten second kill timeout.
	require.Equal(t, `["node", "server.js"]`, shellForm("node server.js"))
	require.Equal(t, `["pnpm", "run", "start"]`, shellForm(" pnpm run start "))
}

func TestShellForm_FallsBackToAShellWhenTheCommandNeedsOne(t *testing.T) {
	t.Parallel()
	require.Equal(t, `["/bin/sh", "-c", "npm run migrate && npm start"]`,
		shellForm("npm run migrate && npm start"))
	require.Contains(t, shellForm("sh -c 'echo $PORT'"), "/bin/sh")
}

func TestShellForm_QuotesArgumentsThatNeedIt(t *testing.T) {
	t.Parallel()
	require.Equal(t, `["python", "-m", "uvicorn", "app:app"]`,
		shellForm("python -m uvicorn app:app"))
	require.Contains(t, shellForm(`node "my file.js"`), `"\"my"`)
}

func TestBuildpackNames_ListsEveryOne(t *testing.T) {
	t.Parallel()
	require.Equal(t, []string{"go", "node", "python", "ruby"}, BuildpackNames())
	require.Len(t, buildpacks, len(BuildpackNames()),
		"a buildpack that is not listed cannot be discovered by anybody")
}

func TestQuote_DoesNotHTMLEscape(t *testing.T) {
	t.Parallel()
	// The default encoder turns & < and > into \u0026 and friends. That is
	// correct JSON and a terrible Dockerfile: it is what somebody reads when
	// they open the generated file to find out what it runs.
	require.Equal(t, `"a b"`, quote("a b"))
	require.Equal(t, `"say \"hi\""`, quote(`say "hi"`))
	require.Equal(t, `"a && b"`, quote("a && b"))
	require.Equal(t, `"a > b < c"`, quote("a > b < c"))
	require.NotContains(t, quote("a && b"), `\u0026`)
}

func TestMajorFromRange_TakesTheFloorOfARange(t *testing.T) {
	t.Parallel()
	require.Equal(t, "20", majorFromRange(">=20.9.0"))
	require.Equal(t, "22", majorFromRange("^22"))
	require.Equal(t, "18", majorFromRange("18.x"))
	require.Equal(t, "", majorFromRange(""))
	require.Equal(t, "", majorFromRange("latest"))
	require.Equal(t, "", majorFromRange("4"), "a version before Node had a runtime worth targeting")
}
