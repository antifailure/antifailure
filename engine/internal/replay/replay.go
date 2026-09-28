// Package replay owns local incident evidence and immutable replay experiments.
package replay

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/pkg/livekey"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

const MaxBytes = 4 << 20

var namePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:-]{0,127}$`)
var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var commitPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)

type Exchange struct {
	Seq        int             `json:"seq"`
	Parent     *int            `json:"parent"`
	Request    json.RawMessage `json:"request"`
	Key        string          `json:"key"`
	Kind       string          `json:"kind"`
	Name       string          `json:"name"`
	Version    string          `json:"version"`
	Response   json.RawMessage `json:"response,omitempty"`
	Error      string          `json:"error,omitempty"`
	CapturedAt string          `json:"capturedAt"`
	DurationMs float64         `json:"durationMs"`
	Provenance string          `json:"provenance"`
	Usage      *Usage          `json:"usage,omitempty"`
}

type Usage struct {
	InputTokens  int64   `json:"inputTokens"`
	OutputTokens int64   `json:"outputTokens"`
	CostUSD      float64 `json:"costUSD"`
}

type Incident struct {
	SchemaVersion int             `json:"schemaVersion"`
	RunID         string          `json:"runId"`
	TraceID       string          `json:"traceId"`
	Project       string          `json:"project"`
	Service       string          `json:"service"`
	Commit        string          `json:"commit"`
	ObservedAt    string          `json:"observedAt"`
	PolicyVersion string          `json:"policyVersion"`
	Input         json.RawMessage `json:"input,omitempty"`
	Output        json.RawMessage `json:"output,omitempty"`
	InputHash     string          `json:"inputHash"`
	Status        string          `json:"status"`
	Issues        []string        `json:"issues"`
	Exchanges     []Exchange      `json:"exchanges"`
	Clock         string          `json:"clock"`
	Identity      string          `json:"identity"`
	Golden        string          `json:"golden"`
	DurationMs    float64         `json:"durationMs"`
}

type Assertion struct {
	Pointer  string          `json:"pointer"`
	Baseline json.RawMessage `json:"baseline"`
	Expected json.RawMessage `json:"expected"`
}

// Scenario is the approved harness. Candidate code never supplies its policy.
type Scenario struct {
	SchemaVersion  int             `json:"schemaVersion"`
	ID             string          `json:"id"`
	IncidentRef    string          `json:"incidentRef"`
	Project        string          `json:"project"`
	Golden         string          `json:"golden"`
	GoldenIdentity string          `json:"goldenIdentity"`
	GoldenAt       time.Time       `json:"goldenAt"`
	Manifest       schema.Manifest `json:"manifest"`
	MaskingRef     string          `json:"maskingRef,omitempty"`
	Endpoint       string          `json:"endpoint"`
	Assertion      Assertion       `json:"assertion"`
	Tables         []string        `json:"tables"`
	CreatedAt      time.Time       `json:"createdAt"`
	Owner          string          `json:"owner"`
}

type Operation struct {
	Seq    int    `json:"seq"`
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Key    string `json:"key"`
	Source string `json:"source"`
}
type Effect struct {
	Name    string          `json:"name"`
	Request json.RawMessage `json:"request"`
}
type Response struct {
	SchemaVersion int             `json:"schemaVersion"`
	Output        json.RawMessage `json:"output,omitempty"`
	Issues        []string        `json:"issues"`
	Operations    []Operation     `json:"operations"`
	Effects       []Effect        `json:"effects"`
	Hits          int             `json:"hits"`
}
type Side struct {
	Commit    string            `json:"commit"`
	Branch    string            `json:"branch"`
	EnvID     string            `json:"envId"`
	Response  *Response         `json:"response,omitempty"`
	TornDown  bool              `json:"tornDown"`
	Assertion bool              `json:"assertion"`
	Egress    []json.RawMessage `json:"egress"`
}
type Report struct {
	SchemaVersion     int               `json:"schemaVersion"`
	ID                string            `json:"id"`
	Scenario          string            `json:"scenario"`
	Verdict           string            `json:"verdict"`
	State             string            `json:"state"`
	Fidelity          string            `json:"fidelity"`
	Dependencies      map[string]string `json:"dependencies"`
	Golden            string            `json:"golden"`
	GoldenAt          time.Time         `json:"goldenAt"`
	ObservedAt        string            `json:"observedAt"`
	TraceID           string            `json:"traceId"`
	CaptureBytes      int               `json:"captureBytes"`
	NewModelCalls     int               `json:"newModelCalls"`
	DurationMs        int64             `json:"durationMs"`
	TeardownManifest  *schema.Manifest  `json:"teardownManifest,omitempty"`
	DatabaseUnchanged bool              `json:"databaseUnchanged"`
	Notes             []string          `json:"notes"`
	Issues            []string          `json:"issues"`
	StartedAt         time.Time         `json:"startedAt"`
	CompletedAt       *time.Time        `json:"completedAt,omitempty"`
	Baseline          Side              `json:"baseline"`
	Candidate         Side              `json:"candidate"`
	Database          json.RawMessage   `json:"database,omitempty"`
}

func Decode(body []byte, out any) error {
	if len(body) > MaxBytes {
		return fmt.Errorf("artifact exceeds %d bytes", MaxBytes)
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return fmt.Errorf("invalid artifact: %w", err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("artifact must contain one JSON document")
	}
	return nil
}

func (i Incident) Validate() error {
	encoded, err := json.Marshal(i)
	if err != nil || len(livekey.Scan(string(encoded), "incident")) > 0 {
		return fmt.Errorf("incident contains a credential or cannot be encoded")
	}
	if i.SchemaVersion != 1 || !namePattern.MatchString(i.RunID) || !namePattern.MatchString(i.Project) || !namePattern.MatchString(i.Service) || !commitPattern.MatchString(i.Commit) || !digestPattern.MatchString(i.InputHash) {
		return fmt.Errorf("incident identity or schema is invalid")
	}
	if len(i.TraceID) != 32 {
		return fmt.Errorf("incident trace ID must be W3C hexadecimal")
	}
	if _, err := hex.DecodeString(i.TraceID); err != nil {
		return fmt.Errorf("incident trace ID is invalid")
	}
	if _, err := time.Parse(time.RFC3339Nano, i.ObservedAt); err != nil {
		return fmt.Errorf("incident time is invalid")
	}
	if i.Status != "complete" && i.Status != "incomplete" {
		return fmt.Errorf("incident status is invalid")
	}
	if len(i.Exchanges) > 10000 {
		return fmt.Errorf("incident contains too many exchanges")
	}
	for n, e := range i.Exchanges {
		if e.Seq != n || !digestPattern.MatchString(e.Key) || !namePattern.MatchString(e.Name) || !namePattern.MatchString(e.Version) || e.Provenance != "recorded" {
			return fmt.Errorf("exchange %d identity is invalid", n)
		}
		switch e.Kind {
		case "model", "tool", "http", "database", "effect":
		default:
			return fmt.Errorf("exchange %d kind is unsupported", n)
		}
	}
	return nil
}

func (i Incident) Missing() []string {
	missing := append([]string{}, i.Issues...)
	if i.Status != "complete" {
		missing = append(missing, "capture_incomplete")
	}
	if len(i.Input) == 0 {
		missing = append(missing, "input_not_captured")
	}
	if i.Identity != "synthetic" {
		missing = append(missing, "identity_mapping_unavailable")
	}
	if i.Clock != "sdk" {
		missing = append(missing, "clock_contract_unsupported")
	}
	for _, e := range i.Exchanges {
		if len(e.Request) == 0 || string(e.Request) == "null" {
			missing = append(missing, fmt.Sprintf("request_not_captured:%d", e.Seq))
		}
		if e.Kind != "database" && len(e.Response) == 0 && e.Error == "" {
			missing = append(missing, fmt.Sprintf("response_not_captured:%d", e.Seq))
		}
	}
	return missing
}

func (s Scenario) Validate() error {
	encoded, encodeErr := json.Marshal(s)
	if encodeErr != nil || len(livekey.Scan(string(encoded), "scenario")) > 0 {
		return fmt.Errorf("scenario contains a credential or cannot be encoded")
	}
	if s.SchemaVersion != 1 || !namePattern.MatchString(s.ID) || !digestPattern.MatchString(s.IncidentRef) || s.Project == "" || s.Project != s.Manifest.Name || s.Golden == "" || s.GoldenIdentity == "" || s.GoldenAt.IsZero() {
		return fmt.Errorf("scenario identity, golden or schema is invalid")
	}
	if s.MaskingRef != "" && !digestPattern.MatchString(s.MaskingRef) {
		return fmt.Errorf("masking artifact reference is invalid")
	}
	if s.Endpoint == "" || !strings.HasPrefix(s.Endpoint, "/") || strings.HasPrefix(s.Endpoint, "//") || strings.ContainsAny(s.Endpoint, "?#\\\r\n") {
		return fmt.Errorf("replay endpoint must be an application path")
	}
	if len(s.Assertion.Baseline) == 0 || len(s.Assertion.Expected) == 0 || !json.Valid(s.Assertion.Baseline) || !json.Valid(s.Assertion.Expected) || Equal(s.Assertion.Baseline, s.Assertion.Expected) {
		return fmt.Errorf("declare distinct original failure and expected outcomes")
	}
	if s.Assertion.Pointer != "" && !strings.HasPrefix(s.Assertion.Pointer, "/") {
		return fmt.Errorf("assertion uses a JSON pointer")
	}
	if len(s.Tables) == 0 || len(s.Tables) > 32 {
		return fmt.Errorf("name between one and 32 relevant database tables")
	}
	return nil
}

func Equal(a, b json.RawMessage) bool {
	var x, y any
	d := json.NewDecoder(bytes.NewReader(a))
	d.UseNumber()
	if d.Decode(&x) != nil {
		return false
	}
	d = json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if d.Decode(&y) != nil {
		return false
	}
	ax, _ := json.Marshal(x)
	by, _ := json.Marshal(y)
	return bytes.Equal(ax, by)
}

func Assert(body json.RawMessage, pointer string, expected json.RawMessage) bool {
	var value any
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	if d.Decode(&value) != nil {
		return false
	}
	if pointer != "" {
		for _, part := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
			key := strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
			obj, ok := value.(map[string]any)
			if !ok {
				return false
			}
			value, ok = obj[key]
			if !ok {
				return false
			}
		}
	}
	b, err := json.Marshal(value)
	return err == nil && Equal(b, expected)
}

type Store struct {
	Root    string
	Project string
}
type Entry struct {
	ID    string          `json:"id"`
	Error string          `json:"error,omitempty"`
	Value json.RawMessage `json:"value,omitempty"`
}

func Digest(body []byte) string { sum := sha256.Sum256(body); return hex.EncodeToString(sum[:]) }
func (s Store) path(kind, id string) (string, error) {
	if !namePattern.MatchString(id) || strings.Contains(id, ":") {
		return "", fmt.Errorf("invalid artifact ID")
	}
	switch kind {
	case "incidents", "scenarios", "attempts", "blobs", "retired":
	default:
		return "", fmt.Errorf("invalid artifact kind")
	}
	return filepath.Join(s.Root, kind, id+".json"), nil
}

func (s Store) PutBlob(body []byte) (string, error) {
	id := Digest(body)
	return id, s.write("blobs", id, body, false)
}
func (s Store) Blob(id string) ([]byte, error) {
	if !digestPattern.MatchString(id) {
		return nil, fmt.Errorf("invalid blob digest")
	}
	body, err := s.Read("blobs", id)
	if err == nil && Digest(body) != id {
		return nil, fmt.Errorf("artifact hash mismatch: %s", id)
	}
	return body, err
}
func (s Store) Put(kind, id string, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.write(kind, id, body, kind == "attempts")
}
func (s Store) write(kind, id string, body []byte, replace bool) error {
	if len(body) > MaxBytes {
		return fmt.Errorf("artifact exceeds byte limit")
	}
	path, err := s.path(kind, id)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".pending-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if err = f.Chmod(0o600); err == nil {
		_, err = f.Write(body)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if replace {
		err = os.Rename(f.Name(), path)
	} else {
		err = os.Link(f.Name(), path)
		if os.IsExist(err) {
			existing, readErr := s.Read(kind, id)
			if readErr == nil && bytes.Equal(existing, body) {
				return nil
			}
			return fmt.Errorf("immutable artifact %s already has different content", id)
		}
	}
	if err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}
func (s Store) Read(kind, id string) ([]byte, error) {
	path, err := s.path(kind, id)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("artifact %s unavailable", id)
	}
	if !info.Mode().IsRegular() || info.Size() > MaxBytes {
		return nil, fmt.Errorf("artifact %s is not a bounded regular file", id)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if len(b) > MaxBytes {
		return nil, fmt.Errorf("artifact exceeds byte limit")
	}
	return b, err
}
func (s Store) List(kind string) ([]Entry, error) {
	path, err := s.path(kind, "check")
	if err != nil {
		return nil, err
	}
	files, err := os.ReadDir(filepath.Dir(path))
	if os.IsNotExist(err) {
		return []Entry{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Entry{}
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(f.Name(), ".json")
		entry := Entry{ID: id}
		b, readErr := s.Read(kind, id)
		if readErr == nil {
			var v any
			readErr = Decode(b, &v)
		}
		if readErr == nil && kind == "incidents" {
			var incident Incident
			readErr = Decode(b, &incident)
			if readErr == nil {
				readErr = incident.Validate()
			}
		}
		if readErr == nil && kind == "scenarios" {
			var scenario Scenario
			readErr = Decode(b, &scenario)
			if readErr == nil {
				readErr = scenario.Validate()
			}
		}
		if readErr == nil {
			entry.Value = b
		} else {
			entry.Error = readErr.Error()
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
