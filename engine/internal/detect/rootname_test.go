package detect_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The repository root reaches detection as an operating system path, and on
// Windows that path has no forward slash in it. A service at the root of a
// Windows checkout was named after the whole path, C:\Users\me\shop becoming
// cusersmeshop, and so was the manifest and every question asked about it.
// Built from filepath.Join so the root is spelled the way this machine spells
// it; on Windows that is the case that broke.
func TestARootServiceIsNamedAfterTheLastElementOfTheRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "shop")
	res := run(t, root, map[string]string{
		"package.json": `{"name": "", "scripts": {"start": "node server.js"}}`,
		"server.js":    "require('http').createServer().listen(3000)\n",
	})
	require.Equal(t, "shop", res.Draft.Name)
	require.NotEmpty(t, res.Draft.Services)
	require.Equal(t, "shop", res.Draft.Services[0].Name)
}
