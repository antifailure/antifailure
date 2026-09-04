package detect_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// This is the Bonfire layout with no credentials or trading implementation.
func TestRuntimeOwnership_HelperImageIsNotAWebService(t *testing.T) {
	res := run(t, "research", map[string]string{
		"dashboard/package.json":           `{"name":"research-dashboard","dependencies":{"next":"15"},"scripts":{"start":"next start -p 3100"}}`,
		"dashboard/Dockerfile":             "FROM node:22\nEXPOSE 3100\nCMD [\"node\",\"server.js\"]\n",
		"engine/agents/sandbox/Dockerfile": "FROM python:3.12-slim\nWORKDIR /sandbox\nUSER nobody\n",
		"alembic.ini":                      "[alembic]\nscript_location = migrations\n",
	})
	require.Len(t, res.Draft.Services, 1)
}

func TestRuntimeOwnership_OrphanMigrationRequiresAnOwner(t *testing.T) {
	res := run(t, "research", map[string]string{
		"dashboard/package.json": `{"name":"dashboard","dependencies":{"next":"15"}}`,
		"alembic.ini":            "[alembic]\n",
	})
	var migration string
	for _, q := range res.Questions {
		if q.ID == "migration.research.service" {
			migration = q.Migration
		}
	}
	require.Equal(t, "alembic upgrade head", migration)
}

func TestRuntimeOwnership_OrphanMigrationIsNotAssignedBySortOrder(t *testing.T) {
	res := run(t, "research", map[string]string{
		"dashboard/package.json": `{"name":"dashboard","dependencies":{"next":"15"}}`,
		"alembic.ini":            "[alembic]\n",
	})
	require.Empty(t, serviceNamed(t, res.Draft, "dashboard").Migrate)
}

func TestRuntimeOwnership_ComposeCanSupplyTheImageCommand(t *testing.T) {
	res := run(t, "research", map[string]string{
		"compose.yaml":       "services:\n  api:\n    build: ./runtime\n    command: python app.py\n    ports:\n      - '8080:8080'\n",
		"runtime/Dockerfile": "FROM python:3.12-slim\nWORKDIR /app\n",
	})
	svc := serviceNamed(t, res.Draft, "api")
	var dockerfile string
	if svc.Build != nil {
		dockerfile = svc.Build.Dockerfile
	}
	require.Equal(t, "runtime/Dockerfile", dockerfile)
}

func TestRuntimeOwnership_UniqueLocalMigrationKeepsItsOwner(t *testing.T) {
	res := run(t, "research", map[string]string{
		"dashboard/package.json": `{"name":"ui","dependencies":{"next":"15"}}`,
		"dashboard/alembic.ini":  "[alembic]\n",
	})
	require.Equal(t, "alembic upgrade head", serviceNamed(t, res.Draft, "ui").Migrate)
}
