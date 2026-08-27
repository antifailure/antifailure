package insights_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	"github.com/antifailure/antifailure/engine/internal/insights"
	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// These tests prove the applier that exists for Rails, Django, Alembic and
// Knex: the tools whose migrations are not SQL anybody else can read, and
// which therefore have to be run by their own tool inside the service's image.
//
// The migrate command here is psql rather than a Rails app, deliberately. What
// has to be proved is the mechanism, and a Rails image would prove the same
// mechanism while taking ten minutes to build and dragging a Ruby toolchain
// into this repository's test suite. The image is real, the command is real,
// the network is real and the database is real.

// dockerBranch is a Postgres container standing in for a provider's branch.
type dockerBranch struct {
	cli  *client.Client
	id   string
	name string
	url  secrets.Value
}

// AttachToNetwork makes dockerBranch an insights.Attachable, the same shape
// the Docker database provider satisfies. Go matches interfaces structurally,
// so this is the contract the real provider is held to and not a parallel one.
func (b *dockerBranch) AttachToNetwork(
	ctx context.Context, ref, networkID, alias string,
) (int, error) {
	err := b.cli.NetworkConnect(ctx, networkID, ref, &network.EndpointSettings{
		Aliases: []string{alias},
	})
	return 5432, err
}

func requireBranchContainer(t *testing.T, name string) (*dockerBranch, func()) {
	t.Helper()
	if os.Getenv("AF_SKIP_DOCKER") != "" {
		t.Skip("skipped: AF_SKIP_DOCKER is set")
	}
	cli, err := dockerutil.Client()
	if err != nil {
		t.Skipf("skipped: no Docker daemon is reachable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)

	full := "af-lane6-" + name
	_ = dockerutil.RemoveContainer(ctx, cli, full)
	created, err := cli.ContainerCreate(ctx,
		&container.Config{
			Image: "postgres:17-alpine",
			Env: []string{
				"POSTGRES_PASSWORD=test", "POSTGRES_USER=postgres", "POSTGRES_DB=postgres",
			},
			Labels: dockerutil.Managed("test", name, time.Now().UTC()),
		},
		&container.HostConfig{RestartPolicy: container.RestartPolicy{Name: "no"}},
		nil, nil, full)
	if err != nil {
		cancel()
		_ = cli.Close()
		t.Skipf("skipped: the branch container could not be created: %v", err)
	}
	stop := func() {
		c, done := context.WithTimeout(context.Background(), 2*time.Minute)
		defer done()
		_ = dockerutil.RemoveContainer(c, cli, created.ID)
		_ = cli.Close()
		cancel()
	}
	if err := cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		stop()
		t.Skipf("skipped: the branch container could not be started: %v", err)
	}

	b := &dockerBranch{cli: cli, id: created.ID, name: full}
	// The container publishes no port, because nothing outside a container
	// needs to reach it: that is the point of the sealed network. The applier
	// rewrites the host anyway, so the string only has to carry the
	// credential and the database name.
	b.url = secrets.New("postgres://postgres:test@127.0.0.1:1/postgres?sslmode=disable")
	return b, stop
}

// waitForBranch blocks until Postgres inside the container answers, asked from
// a throwaway container on a network with it, since there is no published
// port to poll from here.
func waitForBranch(t *testing.T, b *dockerBranch) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	probe, err := b.cli.NetworkCreate(ctx, b.name+"-probe", network.CreateOptions{
		Driver: "bridge", Internal: true,
		Labels: dockerutil.Managed("test", "probe", time.Now().UTC()),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		c, done := context.WithTimeout(context.Background(), time.Minute)
		defer done()
		_ = b.cli.NetworkDisconnect(c, probe.ID, b.id, true)
		_ = b.cli.NetworkRemove(c, probe.ID)
	})
	_, err = b.AttachToNetwork(ctx, b.id, probe.ID, "db")
	require.NoError(t, err)

	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		if code, _ := runOnNetwork(ctx, b.cli, probe.ID,
			"until pg_isready -h db -U postgres -q; do sleep 1; done"); code == 0 {
			return probe.ID
		}
		select {
		case <-ctx.Done():
			t.Skip("skipped: the branch container never became ready")
		case <-time.After(3 * time.Second):
		}
	}
	t.Skip("skipped: the branch container never became ready")
	return ""
}

// runOnNetwork runs one command in a throwaway container on a network and
// returns its exit code and output.
func runOnNetwork(
	ctx context.Context, cli *client.Client, netID, command string,
) (int64, string) {
	created, err := cli.ContainerCreate(ctx,
		&container.Config{
			Image: "postgres:17-alpine",
			Cmd:   []string{"sh", "-lc", command},
			Env:   []string{"PGPASSWORD=test"},
		},
		&container.HostConfig{NetworkMode: container.NetworkMode(netID)},
		nil, nil, "")
	if err != nil {
		return -1, err.Error()
	}
	defer func() {
		c, done := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer done()
		_ = dockerutil.RemoveContainer(c, cli, created.ID)
	}()
	if err := cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return -1, err.Error()
	}
	statusCh, errCh := cli.ContainerWait(ctx, created.ID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		return -1, fmt.Sprint(err)
	case status := <-statusCh:
		return status.StatusCode, ""
	case <-ctx.Done():
		return -1, "timed out"
	}
}

func TestContainerApplier_RunsTheProjectsOwnMigrateCommandInItsImage(t *testing.T) {
	b, done := requireBranchContainer(t, "applier")
	defer done()
	probeNet := waitForBranch(t, b)
	ctx := context.Background()

	// The schema the migration will change, loaded the same way anything else
	// on this network reaches the database.
	code, out := runOnNetwork(ctx, b.cli, probeNet,
		`psql -h db -U postgres -d postgres -v ON_ERROR_STOP=1 `+
			`-c "CREATE TABLE orders (id bigserial primary key, total_cents int not null)"`)
	require.Zero(t, code, out)

	applier := &insights.ContainerApplier{
		Image:       "postgres:17-alpine",
		Command:     `psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -c "ALTER TABLE orders ADD COLUMN currency text"`,
		EnvID:       "lane6applier",
		Database:    b,
		DatabaseRef: b.id,
	}
	_, err := applier.Apply(ctx, b.url, nil)
	require.NoError(t, err)

	// It really ran, against the real database.
	code, out = runOnNetwork(ctx, b.cli, probeNet,
		`psql -h db -U postgres -d postgres -tAc `+
			`"SELECT count(*) FROM information_schema.columns `+
			`WHERE table_name='orders' AND column_name='currency'" | grep -qx 1`)
	require.Zero(t, code, out)
}

func TestContainerApplier_ReportsTheToolsOwnOutputWhenItFails(t *testing.T) {
	b, done := requireBranchContainer(t, "applierfail")
	defer done()
	waitForBranch(t, b)

	// A migration tool explains its failure far better than an exit code
	// does, and discarding that in favour of "exit 1" would make the report
	// useless in exactly the case it exists for.
	applier := &insights.ContainerApplier{
		Image:       "postgres:17-alpine",
		Command:     `psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -c "ALTER TABLE nothing_here ADD COLUMN x text"`,
		EnvID:       "lane6applierfail",
		Database:    b,
		DatabaseRef: b.id,
	}
	_, err := applier.Apply(context.Background(), b.url, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nothing_here",
		"the tool's own message is the finding, not the exit code")
}

func TestContainerApplier_HasNoRouteOffTheMachine(t *testing.T) {
	b, done := requireBranchContainer(t, "appliersealed")
	defer done()
	waitForBranch(t, b)

	// The product's whole premise is that an environment has nowhere to send
	// a packet. This container holds a connection to a copy of production's
	// data, so a rehearsal that quietly ran with the internet attached would
	// be the one hole in that. The network is created internal; this is the
	// test that says so rather than the comment.
	//
	// It asserts the reachable and the unreachable in ONE command, because a
	// command that fails for the wrong reason would pass a test that only
	// checked the second half.
	applier := &insights.ContainerApplier{
		Image: "postgres:17-alpine",
		Command: `psql "$DATABASE_URL" -tAc "SELECT 1" >/dev/null || exit 3; ` +
			`if getent hosts example.com >/dev/null 2>&1; then exit 4; fi; ` +
			`if nc -z -w 3 1.1.1.1 443; then exit 5; fi; exit 0`,
		EnvID:       "lane6sealed",
		Database:    b,
		DatabaseRef: b.id,
	}
	_, err := applier.Apply(context.Background(), b.url, nil)
	require.NoError(t, err,
		"exit 3 means the database was unreachable, 4 that a public name resolved, "+
			"5 that a packet left the machine")
}

func TestContainerApplier_RefusesWithNothingToRun(t *testing.T) {
	t.Parallel()
	// A dead applier would look like a migration that rehearsed cleanly.
	_, err := (&insights.ContainerApplier{}).Apply(context.Background(), secrets.Value{}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no image or migrate command")
}

func TestContainerApplier_LeavesNothingBehind(t *testing.T) {
	b, done := requireBranchContainer(t, "applierclean")
	defer done()
	waitForBranch(t, b)
	ctx := context.Background()

	applier := &insights.ContainerApplier{
		Image:       "postgres:17-alpine",
		Command:     `psql "$DATABASE_URL" -tAc "SELECT 1"`,
		EnvID:       "lane6clean",
		Database:    b,
		DatabaseRef: b.id,
	}
	_, err := applier.Apply(ctx, b.url, nil)
	require.NoError(t, err)

	// A rehearsal that leaves a container holding a connection to a copy of
	// production's data is the leak this product exists to prevent.
	containers, err := b.cli.ContainerList(ctx, container.ListOptions{All: true})
	require.NoError(t, err)
	for _, c := range containers {
		require.NotContains(t, strings.Join(c.Names, " "), "af-rehearse-lane6clean")
	}
	nets, err := b.cli.NetworkList(ctx, network.ListOptions{})
	require.NoError(t, err)
	for _, n := range nets {
		require.NotEqual(t, "af-rehearse-lane6clean", n.Name)
	}
}
