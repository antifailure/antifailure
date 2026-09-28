package local

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	"github.com/antifailure/antifailure/engine/internal/proxyimage"
	"github.com/moby/moby/client"
)

// QuiesceReplay stops only this environment's application containers. Database
// and sidecars stay up for final observations; Down removes stopped services.
func (r *Runtime) QuiesceReplay(ctx context.Context, envID string) error {
	if envID == "" {
		return fmt.Errorf("replay environment identity is absent")
	}
	options := client.ContainerListOptions{All: true, Filters: dockerutil.Filter(dockerutil.LabelEnv, envID, dockerutil.LabelKind, dockerutil.KindService)}
	listed, err := r.cli.ContainerList(ctx, options)
	if err != nil {
		return err
	}
	if len(listed.Items) == 0 {
		return fmt.Errorf("replay application containers are absent")
	}
	for _, item := range listed.Items {
		inspected, inspectErr := r.cli.ContainerInspect(ctx, item.ID, client.ContainerInspectOptions{})
		if inspectErr != nil {
			return inspectErr
		}
		if inspected.Container.Config == nil {
			return fmt.Errorf("replay container has no configuration")
		}
		labels := inspected.Container.Config.Labels
		if !dockerutil.IsOurs(labels) || labels[dockerutil.LabelEnv] != envID || labels[dockerutil.LabelKind] != dockerutil.KindService {
			return fmt.Errorf("replay service ownership changed")
		}
		seconds := 10
		if _, stopErr := r.cli.ContainerStop(ctx, item.ID, client.ContainerStopOptions{Timeout: &seconds}); stopErr != nil {
			return stopErr
		}
	}
	confirmed, err := r.cli.ContainerList(ctx, options)
	if err != nil {
		return err
	}
	for _, item := range confirmed.Items {
		inspected, inspectErr := r.cli.ContainerInspect(ctx, item.ID, client.ContainerInspectOptions{})
		if inspectErr != nil {
			return inspectErr
		}
		if inspected.Container.State == nil || inspected.Container.State.Running || inspected.Container.State.Restarting {
			return fmt.Errorf("replay service did not stop")
		}
	}
	return nil
}

// ReplayDecisions reads all retained evidence and refuses a truncated stream.
// Ordinary interactive log reads keep their existing tail behavior.
func (r *Runtime) ReplayDecisions(ctx context.Context, envID string) ([]Decision, error) {
	id := proxyName(envID)
	if _, err := r.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{}); err != nil {
		return nil, fmt.Errorf("replay sidecar is unavailable: %w", err)
	}
	before, err := r.replayWatermark(ctx, id, envID)
	if err != nil {
		return nil, err
	}
	rc, err := r.cli.ContainerLogs(ctx, id, client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true, Tail: "all"})
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	const limit = 8 << 20
	body, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, err
	}
	if len(body) > limit {
		return nil, fmt.Errorf("replay egress evidence exceeds its byte limit")
	}
	after, err := r.replayWatermark(ctx, id, envID)
	if err != nil {
		return nil, err
	}
	return verifiedReplayDecisions(stripDockerLogFraming(string(body)), before, after)
}

func replayDecisions(body string) ([]Decision, error) {
	out, _, err := parseReplayEvidence(body)
	return out, err
}

func parseReplayEvidence(body string) ([]Decision, uint64, error) {
	out := []Decision{}
	seen := map[uint64]bool{}
	var maximum uint64
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var event struct {
			Event string `json:"event"`
			Seq   uint64 `json:"seq"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return out, maximum, fmt.Errorf("replay egress record is malformed")
		}
		if event.Seq > 0 {
			if seen[event.Seq] {
				return out, maximum, fmt.Errorf("replay egress sequence was repeated")
			}
			seen[event.Seq] = true
			if event.Seq > maximum {
				maximum = event.Seq
			}
		}
		if event.Event != "decision" {
			continue
		}
		var d Decision
		if err := json.Unmarshal([]byte(line), &d); err != nil {
			return out, maximum, fmt.Errorf("replay decision is malformed")
		}
		var err error
		d.At, err = time.Parse(time.RFC3339Nano, d.AtRaw)
		if err != nil || d.Seq == 0 {
			return out, maximum, fmt.Errorf("replay decision has no valid time or sequence")
		}
		out = append(out, d)
	}
	if uint64(len(seen)) != maximum {
		return out, maximum, fmt.Errorf("replay egress evidence has missing records")
	}
	return out, maximum, nil
}

// ReplayAbsent checks only the attempt's containers, networks and volumes,
// including the Docker database provider's. Shared golden images are retained.
func (r *Runtime) ReplayAbsent(ctx context.Context, envID string) error {
	if envID == "" {
		return fmt.Errorf("replay environment identity is absent")
	}
	filter := dockerutil.EnvFilter(envID)
	containers, err := r.cli.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: filter})
	if err != nil {
		return fmt.Errorf("checking remaining containers: %w", err)
	}
	networks, err := r.cli.NetworkList(ctx, client.NetworkListOptions{Filters: filter})
	if err != nil {
		return fmt.Errorf("checking remaining networks: %w", err)
	}
	volumes, err := r.cli.VolumeList(ctx, client.VolumeListOptions{Filters: filter})
	if err != nil {
		return fmt.Errorf("checking remaining volumes: %w", err)
	}
	if len(containers.Items)+len(networks.Items)+len(volumes.Items) > 0 {
		return fmt.Errorf("remaining resources: %d containers, %d networks, %d volumes", len(containers.Items), len(networks.Items), len(volumes.Items))
	}
	return nil
}

// Obtained from the running proxy process, independently of Docker's log stream.
// A final failed write produces no later sequence gap, so logs alone cannot prove
// completeness. The sticky failure bit and attempted sequence close that gap.
type replayWatermark struct {
	Version  int    `json:"version"`
	Env      string `json:"env"`
	Instance string `json:"instance"`
	Sequence uint64 `json:"sequence"`
	Failed   bool   `json:"failed"`
	Active   uint64 `json:"active"`
	Work     uint64 `json:"work"`
}

func verifiedReplayDecisions(body string, before, after replayWatermark) ([]Decision, error) {
	if before != after || before.Version != 1 || before.Env == "" || before.Instance == "" || before.Sequence == 0 || before.Active != 0 || before.Failed {
		return nil, fmt.Errorf("replay evidence writer is incomplete or changed while reading")
	}
	decisions, maximum, err := parseReplayEvidence(body)
	if err != nil {
		return nil, err
	}
	if maximum != before.Sequence {
		return nil, fmt.Errorf("replay evidence is missing its final records")
	}
	return decisions, nil
}

func (r *Runtime) replayWatermark(ctx context.Context, id, envID string) (replayWatermark, error) {
	var status replayWatermark
	created, err := r.cli.ExecCreate(ctx, id, client.ExecCreateOptions{
		Cmd: []string{proxyimage.BinaryPath, "-evidence-status"}, AttachStdout: true, AttachStderr: true, TTY: true,
	})
	if err != nil {
		return status, fmt.Errorf("reading replay evidence watermark: %w", err)
	}
	attached, err := r.cli.ExecAttach(ctx, created.ID, client.ExecAttachOptions{TTY: true})
	if err != nil {
		return status, err
	}
	body, readErr := io.ReadAll(io.LimitReader(attached.Reader, 4097))
	attached.Close()
	if readErr != nil {
		return status, readErr
	}
	if len(body) > 4096 {
		return status, fmt.Errorf("replay evidence watermark is oversized")
	}
	for attempt := 0; ; attempt++ {
		inspection, err := r.cli.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
		if err != nil {
			return status, err
		}
		if !inspection.Running {
			if inspection.ExitCode != 0 {
				return status, fmt.Errorf("replay evidence watermark is unavailable")
			}
			break
		}
		if attempt >= probeReapAttempts {
			return status, fmt.Errorf("replay evidence probe did not finish")
		}
		select {
		case <-ctx.Done():
			return status, ctx.Err()
		case <-r.clock.After(probeReapPause):
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&status); err != nil {
		return status, fmt.Errorf("invalid replay evidence watermark")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return status, fmt.Errorf("invalid replay evidence watermark trailer")
	}
	if status.Version != 1 || status.Env != envID || len(status.Instance) != 32 || status.Sequence == 0 || status.Active != 0 || status.Failed {
		return status, fmt.Errorf("replay evidence writer has failed or is unconfirmed")
	}
	return status, nil
}
