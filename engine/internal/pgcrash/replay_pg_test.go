package pgcrash_test

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/fault"
	"github.com/antifailure/antifailure/engine/internal/pgcrash"
)

// quietDatabase is a Postgres that writes no write ahead log of its own, so
// the last record in the log is the last one a test wrote.
//
// wal_level minimal stops the background writer logging standby snapshots,
// bgwriter_lru_maxpages 0 stops it cleaning buffers, and an hour's checkpoint
// timeout keeps the checkpointer out of the way. Set with ALTER SYSTEM and a
// restart, because the suite's container takes its settings from the image.
func quietDatabase(t *testing.T, cli *client.Client, prefix string) (database, *pgx.Conn) {
	t.Helper()
	envID := prefix + strconv.FormatInt(time.Now().UnixNano()%1_000_000, 36)
	db := startDatabase(t, cli, envID, testKind, "")
	ctx := t.Context()

	conn, err := pgx.Connect(ctx, db.url)
	require.NoError(t, err)
	for _, set := range []string{
		"ALTER SYSTEM SET wal_level = 'minimal'",
		"ALTER SYSTEM SET max_wal_senders = 0",
		"ALTER SYSTEM SET bgwriter_lru_maxpages = 0",
		"ALTER SYSTEM SET checkpoint_timeout = '1h'",
	} {
		_, err := conn.Exec(ctx, set)
		require.NoError(t, err, set)
	}
	require.NoError(t, conn.Close(ctx))
	_, err = cli.ContainerRestart(ctx, db.id, client.ContainerRestartOptions{})
	require.NoError(t, err)
	waitUntilAnswering(t, db.url, 2*time.Minute)

	conn, err = pgx.Connect(ctx, db.url)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	var level string
	require.NoError(t, conn.QueryRow(ctx, "SHOW wal_level").Scan(&level))
	require.Equal(t, "minimal", level, "the database is still writing standby records of its own")
	_, err = conn.Exec(ctx, "CREATE TABLE replay_end (id int PRIMARY KEY)")
	require.NoError(t, err)
	_, err = conn.Exec(ctx, "CHECKPOINT")
	require.NoError(t, err)
	return db, conn
}

// flushed is the database's flush position, which the test requires to be
// the insert position too, so the last record flushed is the last record.
func flushed(t *testing.T, conn *pgx.Conn) (string, uint64) {
	t.Helper()
	var flush, insert string
	require.NoError(t, conn.QueryRow(t.Context(),
		"SELECT pg_current_wal_flush_lsn()::text, pg_current_wal_insert_lsn()::text").Scan(&flush, &insert))
	require.Equal(t, insert, flush, "something wrote past the last flush, so the boundary is not the one under test")
	lsn, err := pgcrash.ParseLSN(flush)
	require.NoError(t, err)
	return flush, lsn
}

// judgeAfterRecovery reads what the database said about its recovery since a
// moment, the way the proof does, and judges it against a flush position.
//
// killedSignal is the signal the container's main process died on, for a
// container killed outright: its postmaster cannot log its own death, and the
// exit status is the evidence the proof reads instead.
func judgeAfterRecovery(t *testing.T, cli *client.Client, db database, since time.Time, flush string, killedSignal int) *pgcrash.Result {
	t.Helper()
	ctx := t.Context()
	waitUntilAnswering(t, db.url, 2*time.Minute)
	sh := shell(t, cli, db)
	log, err := sh.Logs(ctx, since)
	require.NoError(t, err)
	after, err := pgcrash.ReadControlForTest(ctx, runner{sh}, pgData)
	require.NoError(t, err)
	r := &pgcrash.Result{Recovery: pgcrash.ParseRecovery(log), After: after, FlushLSN: flush, KilledSignal: killedSignal}
	pgcrash.JudgeCrashForTest(r)
	t.Logf("flush=%s redo=%s..%s endOfRecoveryRedo=%s control checkpoint=%s redo=%s replayEnd=%s (%s)",
		flush, r.Recovery.RedoStart, r.Recovery.RedoEnd, r.Recovery.EndOfRecoveryRedo,
		after.CheckpointLSN, after.RedoLSN, r.ReplayEnd, r.ReplayEndSource)
	for _, p := range r.Problems {
		t.Logf("PROBLEM    %s: %s", p.Rule, p.Detail)
	}
	for _, p := range r.Unverified {
		t.Logf("UNVERIFIED %s: %s", p.Rule, p.Detail)
	}
	require.True(t, r.Crashed(), "the database did not crash, so nothing here measured a recovery")
	require.True(t, r.Recovery.Replayed(), "the database did not replay")
	return r
}

func rowsIn(t *testing.T, db database) int {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), db.url)
	require.NoError(t, err)
	defer func() { _ = conn.Close(context.Background()) }()
	var n int
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT count(*) FROM replay_end").Scan(&n))
	return n
}

// TestRecovery_AReplayThatEndsAtTheLastFlushIsNotShort reproduces the false
// replay_short that failed main d20ded21f, on purpose rather than by timing.
//
// One commit, whose flush the test reads, is the last record in the log, and
// then the checkpointer is killed. Recovery replays that commit and logs
// "redo done at" with its START, which lies before the flush position the
// client read, because the flush position is its END. The rows all survive,
// and the check used to call the replay short.
func TestRecovery_AReplayThatEndsAtTheLastFlushIsNotShort(t *testing.T) {
	cli := requireDocker(t)
	db, conn := quietDatabase(t, cli, "pgr")
	ctx := t.Context()

	_, err := conn.Exec(ctx, "INSERT INTO replay_end VALUES (1)")
	require.NoError(t, err)
	flush, flushLSN := flushed(t, conn)

	inj, err := fault.New(cli, db.envID)
	require.NoError(t, err)
	since := time.Now()
	_, err = inj.Inject(ctx, fault.Fault{
		Name: "kill-the-checkpointer", Kind: fault.KindProcessKill,
		Target: fault.Target{Role: fault.RoleDatabase}, Process: "postgres: checkpointer",
	})
	require.NoError(t, err)

	r := judgeAfterRecovery(t, cli, db, since, flush, 0)
	lastStart, err := pgcrash.ParseLSN(r.Recovery.RedoEnd)
	require.NoError(t, err)
	require.Less(t, lastStart, flushLSN,
		"the log's redo done position is not before the flush, so this run is not the boundary that failed main")
	require.Equal(t, 1, rowsIn(t, db), "the commit was lost, so this is not the case where nothing was")

	require.NotContains(t, rulesOf(r.Problems), pgcrash.RuleReplayShort,
		"a replay that ended exactly at the last flush, losing nothing, was called short")
}

// TestRecovery_AReplayThatLostFlushedLogIsShort is the arm that keeps the
// check able to say no, with a real short replay rather than a constructed
// number.
//
// The log is flushed well past one commit, the container is killed, and the
// segment is zeroed from the end of that commit onwards before the database
// starts again: storage that dropped what it had acknowledged as flushed.
// Recovery ends at that commit, the rows written after it are gone, and the
// check must call the replay short.
//
// synchronous_commit off, which the lost commit arm uses, cannot do this: what
// it loses is past the flush position by definition, so the flush a writer
// read is always replayed. It is caught as a lost commit, and it still is.
func TestRecovery_AReplayThatLostFlushedLogIsShort(t *testing.T) {
	cli := requireDocker(t)
	db, conn := quietDatabase(t, cli, "pgs")
	ctx := t.Context()

	_, err := conn.Exec(ctx, "INSERT INTO replay_end VALUES (1)")
	require.NoError(t, err)
	var cutFile string
	var cutOffset int64
	require.NoError(t, conn.QueryRow(ctx,
		"SELECT file_name, file_offset FROM pg_walfile_name_offset(pg_current_wal_flush_lsn())").Scan(&cutFile, &cutOffset))
	for i := 2; i <= 200; i++ {
		_, err := conn.Exec(ctx, "INSERT INTO replay_end VALUES ($1)", i)
		require.NoError(t, err)
	}
	flush, flushLSN := flushed(t, conn)
	var lastFile string
	require.NoError(t, conn.QueryRow(ctx, "SELECT pg_walfile_name(pg_current_wal_flush_lsn())").Scan(&lastFile))
	require.Equal(t, cutFile, lastFile, "the log crossed a segment, so zeroing one would not cut it where intended")

	since := time.Now()
	_, err = cli.ContainerKill(ctx, db.id, client.ContainerKillOptions{Signal: "KILL"})
	require.NoError(t, err)
	waitStopped(t, cli, db.id)
	zeroFrom(t, cli, db.id, pgData+"/pg_wal/"+cutFile, cutOffset)
	_, err = cli.ContainerStart(ctx, db.id, client.ContainerStartOptions{})
	require.NoError(t, err)

	r := judgeAfterRecovery(t, cli, db, since, flush, 9)
	require.Equal(t, 1, rowsIn(t, db), "the rows past the cut survived, so no log was lost")
	end, err := pgcrash.ParseLSN(r.ReplayEnd)
	require.NoError(t, err, "the end of replay was not established")
	require.Less(t, end, flushLSN)

	require.Contains(t, rulesOf(r.Problems), pgcrash.RuleReplayShort,
		"a replay that ended before the flush a client read, with 199 commits gone, was not called short")
}

func waitStopped(t *testing.T, cli *client.Client, id string) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		r, err := cli.ContainerInspect(t.Context(), id, client.ContainerInspectOptions{})
		require.NoError(t, err)
		if r.Container.State != nil && !r.Container.State.Running {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the database container did not stop")
}

// zeroFrom rewrites one file in a stopped container with every byte from
// offset onwards set to zero, keeping its owner and mode.
func zeroFrom(t *testing.T, cli *client.Client, id, path string, offset int64) {
	t.Helper()
	ctx := t.Context()
	got, err := cli.CopyFromContainer(ctx, id, client.CopyFromContainerOptions{SourcePath: path})
	require.NoError(t, err)
	defer func() { _ = got.Content.Close() }()
	tr := tar.NewReader(got.Content)
	hdr, err := tr.Next()
	require.NoError(t, err)
	data, err := io.ReadAll(tr)
	require.NoError(t, err)
	require.Less(t, offset, int64(len(data)))
	for i := offset; i < int64(len(data)); i++ {
		data[i] = 0
	}

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name: hdr.Name, Mode: hdr.Mode, Size: int64(len(data)),
		Uid: hdr.Uid, Gid: hdr.Gid, ModTime: hdr.ModTime, Typeflag: tar.TypeReg,
	}))
	_, err = tw.Write(data)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	dir := path[:len(path)-len(hdr.Name)]
	_, err = cli.CopyToContainer(ctx, id, client.CopyToContainerOptions{
		DestinationPath: dir, Content: &buf, CopyUIDGID: true,
	})
	require.NoError(t, err)
}
