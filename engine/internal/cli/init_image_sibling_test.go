package cli_test

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// An application beside an image nobody builds is the ordinary compose file:
// the app, and a helper pulled from a registry. af init refused it outright.
//
// Compose reported the image with the build context it does not have, which
// is empty, and empty is how the repository root is spelled. So the image sat
// in the application's directory. Beside `web: build: .` that made compose the
// source of two services at the root, the fold that joins web to its
// Dockerfile and package.json refused, and three services claimed port 3000:
// AF-DET-004 with no default command, or AF-DET-005 once a command was given,
// and nothing written. With no build service beside it the image was folded
// into the application instead, so it vanished from the manifest.
//
// These run the real command over a real directory and read the file it
// wrote, because detection's own tests stop at detect.Run, before the
// validation that refuses the draft.

type writtenService struct {
	Name       string
	Kind       schema.ServiceKind
	Port       int
	Path       string
	Command    string
	Strategy   schema.BuildStrategy
	Dockerfile string
	Image      string
}

func imageSiblingRepo(t *testing.T, compose string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range map[string]string{
		"package.json":       `{"name":"shopfront","scripts":{"start":"next start"},"dependencies":{"next":"15.0.0"}}`,
		"Dockerfile":         "FROM node:20\nEXPOSE 3000\nCMD [\"node\", \"server.js\"]\n",
		"docker-compose.yml": compose,
	} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600))
	}
	return dir
}

func writtenServices(t *testing.T, dir string) []writtenService {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, "antifailure.yaml"))
	require.NoError(t, err, "af init reported success and wrote no manifest")
	var m schema.Manifest
	require.NoError(t, yaml.Unmarshal(body, &m))
	out := make([]writtenService, 0, len(m.Services))
	for _, s := range m.Services {
		w := writtenService{Name: s.Name, Kind: s.Kind, Port: s.Port, Path: s.Path, Command: s.Command}
		if s.Build != nil {
			w.Strategy, w.Dockerfile, w.Image = s.Build.Strategy, s.Build.Dockerfile, s.Build.Image
		}
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// theApplication is the one service the package.json, the Dockerfile and a
// compose `build: .` all describe.
var theApplication = writtenService{
	Name: "shopfront", Kind: schema.ServiceWeb, Port: 3000, Command: "node server.js",
	Strategy: schema.BuildDockerfile, Dockerfile: "Dockerfile",
}

func TestInitImageSibling_EveryShapeWritesTheServicesComposeDeclares(t *testing.T) {
	t.Parallel()
	for _, cell := range []struct {
		name    string
		compose string
		want    []writtenService
	}{
		{
			name: "a build service beside an image",
			compose: `services:
  web:
    build: .
    ports:
      - "3000:3000"
  tools:
    image: acme/rulestools:2.1
`,
			want: []writtenService{theApplication,
				{Name: "tools", Kind: schema.ServiceWorker, Strategy: schema.BuildImage, Image: "acme/rulestools:2.1"}},
		},
		{
			// The shape that refused with AF-DET-005 rather than AF-DET-004.
			name: "a build service with its own command beside an image",
			compose: `services:
  web:
    build: .
    command: node server.js
    ports:
      - "3000:3000"
  tools:
    image: acme/rulestools:2.1
`,
			want: []writtenService{theApplication,
				{Name: "tools", Kind: schema.ServiceWorker, Strategy: schema.BuildImage, Image: "acme/rulestools:2.1"}},
		},
		{
			// It used to be folded into the application and disappear.
			name: "an image and no build service",
			compose: `services:
  tools:
    image: acme/rulestools:2.1
`,
			want: []writtenService{theApplication,
				{Name: "tools", Kind: schema.ServiceWorker, Strategy: schema.BuildImage, Image: "acme/rulestools:2.1"}},
		},
		{
			// The fold the image was blocking still happens when there is none.
			name: "a build service and no image",
			compose: `services:
  web:
    build: .
    ports:
      - "3000:3000"
`,
			want: []writtenService{theApplication},
		},
		{
			// Compose tags the image it builds with the name under image. That
			// is still the application, not a prebuilt image beside it, and
			// only the build key can tell the two apart.
			name: "a build service that also names the image it builds",
			compose: `services:
  web:
    build: .
    image: acme/shopfront:dev
    ports:
      - "3000:3000"
`,
			want: []writtenService{theApplication},
		},
		{
			// One image publishes a port and is a web service; one carries a
			// command and publishes nothing, so it is a worker running it.
			name: "two images beside a build service",
			compose: `services:
  web:
    build: .
    ports:
      - "3000:3000"
  tools:
    image: acme/rulestools:2.1
    command: rules watch
  mail:
    image: axllent/mailpit:v1.20
    ports:
      - "8025:8025"
`,
			want: []writtenService{
				{Name: "mail", Kind: schema.ServiceWeb, Port: 8025, Strategy: schema.BuildImage, Image: "axllent/mailpit:v1.20"},
				theApplication,
				{Name: "tools", Kind: schema.ServiceWorker, Command: "rules watch", Strategy: schema.BuildImage, Image: "acme/rulestools:2.1"},
			},
		},
	} {
		t.Run(cell.name, func(t *testing.T) {
			t.Parallel()
			dir := imageSiblingRepo(t, cell.compose)
			got := runCLI(t, dir, nil, "init", "--non-interactive")
			require.Equal(t, 0, got.code, "af init refused:\n%s%s", got.stdout, got.stderr)
			require.Equal(t, cell.want, writtenServices(t, dir))

			// af init must never write a file af up would then refuse.
			explained := runCLI(t, dir, nil, "explain")
			require.Zero(t, explained.code, explained.stderr)
		})
	}
}

func TestInitImageSibling_TheSummaryNamesTheImageAServiceRuns(t *testing.T) {
	t.Parallel()
	// An image service with no command is not missing one, and a blank cell
	// in the table reads as though it were.
	dir := imageSiblingRepo(t, `services:
  web:
    build: .
    ports:
      - "3000:3000"
  tools:
    image: acme/rulestools:2.1
`)
	got := runCLI(t, dir, nil, "init", "--non-interactive")
	require.Equal(t, 0, got.code, got.stderr)
	require.Contains(t, got.stdout, "image acme/rulestools:2.1",
		"the summary row for an image service must name the image it runs")
}
