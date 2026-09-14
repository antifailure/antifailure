// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
package cloudsql_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antifailure/antifailure/ee/engine/db/cloudsql"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/secret"
	"github.com/stretchr/testify/require"
)

func responseJSON(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestEveryInheritedLoginIsReplacedBeforePublication(t *testing.T) {
	server := newFake(t, seedSQL)
	const password = "AF_FAKE_SOURCE_READER_PASSWORD"
	const token = "AF_FAKE_SOURCE_IAM_TOKEN"
	require.NoError(t, server.AddUser(sourceInstance, "app_reader", "BUILT_IN", password))
	require.NoError(t, server.AddUser(sourceInstance, "person@example.test", "CLOUD_IAM_USER", token))
	for user, credential := range map[string]string{"app_reader": password, "person@example.test": token} {
		url, err := server.UserURL(sourceInstance, user, credential)
		require.NoError(t, err)
		require.NoError(t, reachable(url))
	}
	p := newProvider(t, server)
	golden, err := p.RefreshGolden(context.Background(), goldenSpec())
	require.NoError(t, err)
	branch, err := p.Branch(context.Background(), golden.ID, "all-users")
	require.NoError(t, err)
	for _, name := range []string{golden.ProviderRef, branch.ProviderRef} {
		require.False(t, server.UserPasswordMatches(name, "app_reader", password), "the secondary API password was not rotated")
		for user, credential := range map[string]string{"app_reader": password, "person@example.test": token} {
			url, err := server.UserURL(name, user, credential)
			require.NoError(t, err)
			require.Error(t, reachable(url), "an inherited login still authenticates on %s", name)
		}
		req, err := http.NewRequest(http.MethodGet, server.URL()+"/v1/projects/"+testProject+"/instances/"+name, nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer AF_FAKE_CLOUDSQL_TOKEN")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		var body struct {
			Settings struct {
				Flags []struct{ Name, Value string } `json:"databaseFlags"`
			}
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
		_ = resp.Body.Close()
		preserved := false
		iamDisabled := false
		for _, flag := range body.Settings.Flags {
			if flag.Name == "cloudsql.iam_authentication" && flag.Value == "off" {
				iamDisabled = true
			}
			if flag.Name == "log_min_duration_statement" && flag.Value == "1234" {
				preserved = true
			}
		}
		require.True(t, preserved, "disabling inherited IAM discarded an unrelated database flag")
		require.True(t, iamDisabled, "IAM must remain disabled so a copied group cannot admit a new member after existing roles were sanitized")
	}
	url, err := p.ConnString(context.Background(), branch, provider.ConnDirect)
	require.NoError(t, err)
	require.NoError(t, reachable(url.Reveal()))
	for user, credential := range map[string]string{"app_reader": password, "person@example.test": token} {
		url, err := server.UserURL(sourceInstance, user, credential)
		require.NoError(t, err)
		require.NoError(t, reachable(url), "source login changed")
	}
}

func TestPasswordOperationsFinishBeforeMaskOrBranchPublication(t *testing.T) {
	for _, branching := range []bool{false, true} {
		for _, secondary := range []bool{false, true} {
			t.Run(fmt.Sprintf("branch=%t/secondary=%t", branching, secondary), func(t *testing.T) {
				server := newFake(t, seedSQL)
				require.NoError(t, server.AddUser(sourceInstance, "app_reader", "BUILT_IN", "AF_FAKE_READER_PASSWORD"))
				base := newProvider(t, server)
				version := ""
				if branching {
					g, err := base.RefreshGolden(context.Background(), goldenSpec())
					require.NoError(t, err)
					version = g.ID
				}
				entered := make(chan struct{})
				release := make(chan struct{})
				var once sync.Once
				t.Cleanup(func() { once.Do(func() { close(release) }) })
				opts := options(t, server)
				opts.HTTPClient = cancellingTransport(func(req *http.Request) (*http.Response, error) {
					if strings.HasSuffix(req.URL.Path, "/operations/held-password") {
						select {
						case <-entered:
						default:
							close(entered)
						}
						select {
						case <-release:
							return responseJSON(`{"name":"held-password","status":"DONE"}`), nil
						case <-req.Context().Done():
							return nil, req.Context().Err()
						}
					}
					resp, err := http.DefaultClient.Do(req)
					if err == nil && req.Method == http.MethodPut && strings.HasSuffix(req.URL.Path, "/users") && (req.URL.Query().Get("name") == "app_reader") == secondary {
						_ = resp.Body.Close()
						return responseJSON(`{"name":"held-password","status":"PENDING"}`), nil
					}
					return resp, err
				})
				p, err := newScoped(context.Background(), server, opts)
				require.NoError(t, err)
				defer func() { _ = p.Close() }()
				var masked atomic.Bool
				spec := goldenSpec()
				mask := spec.Mask
				spec.Mask = func(ctx context.Context, u secret.Value) error { masked.Store(true); return mask(ctx, u) }
				done := make(chan error, 1)
				go func() {
					if branching {
						_, err := p.Branch(context.Background(), version, "held-password")
						done <- err
					} else {
						_, err := p.RefreshGolden(context.Background(), spec)
						done <- err
					}
				}()
				select {
				case <-entered:
				case err := <-done:
					t.Fatalf("returned before waiting for the password operation: %v", err)
				case <-time.After(10 * time.Second):
					t.Fatal("password operation was never awaited")
				}
				require.False(t, masked.Load(), "mask started before the password operation completed")
				select {
				case err := <-done:
					t.Fatalf("published before the password operation completed: %v", err)
				default:
				}
				once.Do(func() { close(release) })
				require.NoError(t, <-done)
			})
		}
	}
}

func TestRetryRequiresMatchingCompletedPreparation(t *testing.T) {
	for _, mutation := range []string{"prepared", "env", "version", "state", "source", "golden", "name", "requested_version", "key"} {
		t.Run(mutation, func(t *testing.T) {
			server := newFake(t, seedSQL)
			base := newProvider(t, server)
			g, err := base.RefreshGolden(context.Background(), goldenSpec())
			require.NoError(t, err)
			branch, err := base.Branch(context.Background(), g.ID, "retry")
			require.NoError(t, err)
			opts := options(t, server)
			var writes atomic.Int32
			if mutation == "key" {
				opts.BranchKey = secret.New("AF_FAKE_DIFFERENT_BRANCH_KEY")
			}
			opts.HTTPClient = cancellingTransport(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodGet {
					writes.Add(1)
				}
				resp, err := http.DefaultClient.Do(req)
				if err != nil || req.Method != http.MethodGet || !strings.HasSuffix(req.URL.Path, "/instances/"+branch.ProviderRef) {
					return resp, err
				}
				var data map[string]any
				if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
					return nil, err
				}
				_ = resp.Body.Close()
				labels := data["settings"].(map[string]any)["userLabels"].(map[string]any)
				switch mutation {
				case "prepared":
					delete(labels, "antifailure-prepared")
				case "env":
					labels["antifailure-env"] = "someone-else"
				case "version":
					labels["antifailure-from"] = "another-version"
				case "state":
					data["state"] = "PENDING_CREATE"
				case "name":
					data["name"] = "another-instance"
				case "source":
					labels["antifailure-source"] = "another-source"
				case "golden":
					labels["antifailure-golden"] = "not-a-branch"
				}
				encoded, err := json.Marshal(data)
				if err != nil {
					return nil, err
				}
				return responseJSON(string(encoded)), nil
			})
			p, err := newScoped(context.Background(), server, opts)
			require.NoError(t, err)
			defer func() { _ = p.Close() }()
			requested := g.ID
			if mutation == "requested_version" {
				requested = "gv_different"
			}
			_, err = p.Branch(context.Background(), requested, "retry")
			require.Error(t, err)
			_, err = p.ConnString(context.Background(), provider.Branch{ProviderRef: branch.ProviderRef, EnvID: "retry", From: requested}, provider.ConnDirect)
			require.Error(t, err)
			require.Zero(t, writes.Load(), "a mismatched retry rewrote or removed the existing resource")
			require.Equal(t, 3, server.ResourceCount())
		})
	}
}

type unreadableBody struct{}

func (unreadableBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (unreadableBody) Close() error             { return nil }

func TestAcceptedCreateWithUnreadableResponseIsRemoved(t *testing.T) {
	for _, branching := range []bool{false, true} {
		for _, malformed := range []bool{false, true} {
			t.Run(fmt.Sprintf("branch=%t/malformed=%t", branching, malformed), func(t *testing.T) {
				server := newFake(t, seedSQL)
				base := newProvider(t, server)
				version := ""
				if branching {
					g, err := base.RefreshGolden(context.Background(), goldenSpec())
					require.NoError(t, err)
					version = g.ID
				}
				before := server.ResourceCount()
				opts := options(t, server)
				opts.HTTPClient = cancellingTransport(func(req *http.Request) (*http.Response, error) {
					resp, err := http.DefaultClient.Do(req)
					if err == nil && req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/clone") {
						_ = resp.Body.Close()
						if malformed {
							resp.Body = io.NopCloser(strings.NewReader("{"))
						} else {
							resp.Body = unreadableBody{}
						}
					}
					return resp, err
				})
				p, err := newScoped(context.Background(), server, opts)
				require.NoError(t, err)
				defer func() { _ = p.Close() }()
				if branching {
					_, err = p.Branch(context.Background(), version, "unreadable")
				} else {
					_, err = p.RefreshGolden(context.Background(), goldenSpec())
				}
				require.Error(t, err)
				require.Equal(t, before, server.ResourceCount(), "accepted creation survived an unreadable response")
			})
		}
	}
}

func TestUnknownCreateAcceptanceReturnsAHandleWithoutDeleting(t *testing.T) {
	for _, branching := range []bool{false, true} {
		t.Run(fmt.Sprintf("branch=%t", branching), func(t *testing.T) {
			server := newFake(t, seedSQL)
			base := newProvider(t, server)
			version := ""
			if branching {
				g, err := base.RefreshGolden(context.Background(), goldenSpec())
				require.NoError(t, err)
				version = g.ID
			}
			before := server.ResourceCount()
			opts := options(t, server)
			var deletes atomic.Int32
			opts.HTTPClient = cancellingTransport(func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodDelete {
					deletes.Add(1)
				}
				resp, err := http.DefaultClient.Do(req)
				if err == nil && req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/clone") {
					_ = resp.Body.Close()
					return nil, errors.New("accepted response lost before any status was received")
				}
				return resp, err
			})
			p, err := newScoped(context.Background(), server, opts)
			require.NoError(t, err)
			defer func() { _ = p.Close() }()
			handle := ""
			if branching {
				b, e := p.Branch(context.Background(), version, "unknown")
				handle = b.ProviderRef
				err = e
			} else {
				g, e := p.RefreshGolden(context.Background(), goldenSpec())
				handle = g.ProviderRef
				err = e
			}
			require.Error(t, err)
			require.NotEmpty(t, handle)
			require.Zero(t, deletes.Load())
			require.Equal(t, before+1, server.ResourceCount())
			require.ErrorIs(t, p.Destroy(context.Background(), provider.Branch{ProviderRef: handle}), cloudsql.ErrNotOurs)
			require.Zero(t, deletes.Load(), "an uncertain handle was mistaken for ownership")
		})
	}
}

func TestSourceScopePreventsAdoptionDeletionAndInventoryLeakage(t *testing.T) {
	server := newFake(t, seedSQL)
	first := newProvider(t, server)
	g, err := first.RefreshGolden(context.Background(), goldenSpec())
	require.NoError(t, err)
	b, err := first.Branch(context.Background(), g.ID, "scope")
	require.NoError(t, err)
	opts := options(t, server)
	opts.SourceInstance = "another-source"
	second, err := newScoped(context.Background(), server, opts)
	require.NoError(t, err)
	defer func() { _ = second.Close() }()
	require.ErrorIs(t, second.Destroy(context.Background(), b), cloudsql.ErrNotOurs)
	inventory, err := second.Inventory(context.Background())
	require.NoError(t, err)
	require.Empty(t, inventory)
	goldens, err := second.ListGoldens(context.Background())
	require.NoError(t, err)
	require.Empty(t, goldens)
	_, err = second.Branch(context.Background(), g.ID, "scope")
	require.Error(t, err)
	require.Equal(t, 3, server.ResourceCount())
}

// Done marks the point the second branch enters its cancellable admission wait.
// Its HTTP transport has its own signal, so bypassing admission is observable
// without relying on a sleep to infer that a request did not happen.
type admissionContext struct {
	context.Context
	once    sync.Once
	waiting chan struct{}
}

func (c *admissionContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestAdmissionSerializesIndependentProviderObjects(t *testing.T) {
	server := newFake(t, seedSQL)
	base := newProvider(t, server)
	g, err := base.RefreshGolden(context.Background(), goldenSpec())
	require.NoError(t, err)
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	opts := options(t, server)
	opts.MaxBranches = 1
	opts.HTTPClient = cancellingTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/clone") {
			close(entered)
			<-release
		}
		return http.DefaultClient.Do(req)
	})
	first, err := newScoped(context.Background(), server, opts)
	require.NoError(t, err)
	defer func() { _ = first.Close() }()
	firstDone := make(chan error, 1)
	go func() { _, err := first.Branch(context.Background(), g.ID, "first-admission"); firstDone <- err }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("first clone never started")
	}
	secondHTTP := make(chan struct{})
	var once sync.Once
	opts.HTTPClient = cancellingTransport(func(req *http.Request) (*http.Response, error) {
		once.Do(func() { close(secondHTTP) })
		<-release
		return http.DefaultClient.Do(req)
	})
	second, err := newScoped(context.Background(), server, opts)
	require.NoError(t, err)
	defer func() { _ = second.Close() }()
	ctx := &admissionContext{Context: context.Background(), waiting: make(chan struct{})}
	secondDone := make(chan error, 1)
	go func() { _, err := second.Branch(ctx, g.ID, "second-admission"); secondDone <- err }()
	select {
	case <-ctx.waiting:
	case <-secondHTTP:
		t.Error("second provider read admission state while the first create was unfinished")
	case <-time.After(10 * time.Second):
		t.Error("second provider never entered admission")
	}
	select {
	case <-secondHTTP:
		t.Error("second provider reached HTTP before admission was released")
	default:
	}
	releaseOnce.Do(func() { close(release) })
	require.NoError(t, <-firstDone)
	require.Error(t, <-secondDone, "two provider objects exceeded their shared branch cap")
	require.Equal(t, 3, server.ResourceCount())
}

func TestHookCreatedLoginsAreSanitizedBeforePublication(t *testing.T) {
	for _, hook := range []string{"load", "verify"} {
		t.Run(hook, func(t *testing.T) {
			server := newFake(t, seedSQL)
			opts := options(t, server)
			var target string
			opts.HTTPClient = cancellingTransport(func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/clone") {
					body, err := io.ReadAll(req.Body)
					if err != nil {
						return nil, err
					}
					req.Body = io.NopCloser(strings.NewReader(string(body)))
					var data struct {
						CloneContext struct {
							Destination string `json:"destinationInstanceName"`
						}
					}
					if err := json.Unmarshal(body, &data); err != nil {
						return nil, err
					}
					target = data.CloneContext.Destination
				}
				return http.DefaultClient.Do(req)
			})
			p, err := newScoped(context.Background(), server, opts)
			require.NoError(t, err)
			defer func() { _ = p.Close() }()
			add := func() error { return server.AddUser(target, "hook_reader", "BUILT_IN", "AF_FAKE_HOOK_PASSWORD") }
			spec := goldenSpec()
			if hook == "load" {
				spec.Load = func(context.Context, secret.Value, secret.Value) error { return add() }
			} else {
				verify := spec.Verify
				spec.Verify = func(ctx context.Context, u secret.Value) (string, error) {
					if err := add(); err != nil {
						return "", err
					}
					return verify(ctx, u)
				}
			}
			g, err := p.RefreshGolden(context.Background(), spec)
			require.NoError(t, err)
			url, err := server.UserURL(g.ProviderRef, "hook_reader", "AF_FAKE_HOOK_PASSWORD")
			require.NoError(t, err)
			require.Error(t, reachable(url), "a hook restored a credential after preparation")
		})
	}
}

func TestFailedPasswordOperationNeverPublishes(t *testing.T) {
	for _, branching := range []bool{false, true} {
		t.Run(fmt.Sprintf("branch=%t", branching), func(t *testing.T) {
			server := newFake(t, seedSQL)
			base := newProvider(t, server)
			version := ""
			if branching {
				g, err := base.RefreshGolden(context.Background(), goldenSpec())
				require.NoError(t, err)
				version = g.ID
			}
			before := server.ResourceCount()
			opts := options(t, server)
			opts.HTTPClient = cancellingTransport(func(req *http.Request) (*http.Response, error) {
				resp, err := http.DefaultClient.Do(req)
				if err == nil && req.Method == http.MethodPut && strings.HasSuffix(req.URL.Path, "/users") {
					_ = resp.Body.Close()
					return responseJSON(`{"name":"failed-password","status":"DONE","error":{"errors":[{"code":"INVALID_PASSWORD","message":"rotation failed"}]}}`), nil
				}
				return resp, err
			})
			p, err := newScoped(context.Background(), server, opts)
			require.NoError(t, err)
			defer func() { _ = p.Close() }()
			if branching {
				_, err = p.Branch(context.Background(), version, "failed-password")
			} else {
				_, err = p.RefreshGolden(context.Background(), goldenSpec())
			}
			require.ErrorContains(t, err, "rotation failed")
			require.Equal(t, before, server.ResourceCount(), "failed credential rotation left a published instance")
		})
	}
}

func TestDestroyRequiresTheResourceKindAndEnvironment(t *testing.T) {
	server := newFake(t, seedSQL)
	p := newProvider(t, server)
	g, err := p.RefreshGolden(context.Background(), goldenSpec())
	require.NoError(t, err)
	b, err := p.Branch(context.Background(), g.ID, "owns-this")
	require.NoError(t, err)
	wrong := b
	wrong.EnvID = "another-environment"
	require.ErrorIs(t, p.Destroy(context.Background(), wrong), cloudsql.ErrNotOurs)
	require.ErrorIs(t, p.Destroy(context.Background(), provider.Branch{EnvID: "owns-this", ProviderRef: g.ProviderRef}), cloudsql.ErrNotOurs)
	require.Equal(t, 3, server.ResourceCount(), "branch deletion removed a different environment or a golden")
	require.NoError(t, p.Destroy(context.Background(), b))
	require.NoError(t, p.DestroyGolden(context.Background(), g.ID))
	require.Equal(t, 1, server.ResourceCount())
}

func TestIAMDisableCompletesBeforePasswordRotation(t *testing.T) {
	server := newFake(t, seedSQL)
	opts := options(t, server)
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	var passwords atomic.Int32
	opts.HTTPClient = cancellingTransport(func(req *http.Request) (*http.Response, error) {
		if strings.HasSuffix(req.URL.Path, "/operations/held-iam") {
			select {
			case <-entered:
			default:
				close(entered)
			}
			select {
			case <-release:
				return responseJSON(`{"name":"held-iam","status":"DONE"}`), nil
			case <-req.Context().Done():
				return nil, req.Context().Err()
			}
		}
		hold := false
		if req.Method == http.MethodPatch && req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			data, err := io.ReadAll(body)
			_ = body.Close()
			if err != nil {
				return nil, err
			}
			hold = strings.Contains(string(data), `"databaseFlags"`)
		}
		if req.Method == http.MethodPut && strings.HasSuffix(req.URL.Path, "/users") {
			passwords.Add(1)
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil && hold {
			_ = resp.Body.Close()
			return responseJSON(`{"name":"held-iam","status":"PENDING"}`), nil
		}
		return resp, err
	})
	p, err := newScoped(context.Background(), server, opts)
	require.NoError(t, err)
	defer func() { _ = p.Close() }()
	done := make(chan error, 1)
	go func() { _, err := p.RefreshGolden(context.Background(), goldenSpec()); done <- err }()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("IAM disable was never awaited: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("IAM disable never reached operation polling")
	}
	require.Zero(t, passwords.Load(), "credential rotation started while IAM still had access")
	once.Do(func() { close(release) })
	require.NoError(t, <-done)
	require.Positive(t, passwords.Load())
}

func TestSeparateSourcesCanUseTheSameEnvironment(t *testing.T) {
	server := newFake(t, seedSQL)
	req, err := http.NewRequest(http.MethodPost, server.URL()+"/v1/projects/"+testProject+"/instances/"+sourceInstance+"/clone", strings.NewReader(`{"cloneContext":{"destinationInstanceName":"source-two"}}`))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer AF_FAKE_CLOUDSQL_TOKEN")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
	one := newProvider(t, server)
	opts := options(t, server)
	opts.SourceInstance = "source-two"
	two, err := newScoped(context.Background(), server, opts)
	require.NoError(t, err)
	defer func() { _ = two.Close() }()
	first, err := one.RefreshGolden(context.Background(), goldenSpec())
	require.NoError(t, err)
	second, err := two.RefreshGolden(context.Background(), goldenSpec())
	require.NoError(t, err)
	b1, err := one.Branch(context.Background(), first.ID, "same-env")
	require.NoError(t, err)
	b2, err := two.Branch(context.Background(), second.ID, "same-env")
	require.NoError(t, err)
	require.NotEqual(t, b1.ProviderRef, b2.ProviderRef)
	require.ErrorIs(t, two.Destroy(context.Background(), b1), cloudsql.ErrNotOurs)
	for _, pair := range []struct {
		p *cloudsql.Provider
		b provider.Branch
	}{{one, b1}, {two, b2}} {
		url, err := pair.p.ConnString(context.Background(), pair.b, provider.ConnDirect)
		require.NoError(t, err)
		require.NoError(t, reachable(url.Reveal()))
	}
}

// A real service can enforce complexity and password reuse. Reasserting the
// credential policy after hooks must not blindly rewrite unchanged passwords.
func TestInheritedPasswordPolicyDoesNotRejectRoutinePreparation(t *testing.T) {
	server := newFake(t, seedSQL)
	opts := options(t, server)
	history := map[string]map[string]bool{}
	opts.HTTPClient = cancellingTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodPut && strings.HasSuffix(req.URL.Path, "/users") {
			payload, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			req.Body = io.NopCloser(strings.NewReader(string(payload)))
			var data struct{ Name, Password string }
			if err := json.Unmarshal(payload, &data); err != nil {
				return nil, err
			}
			upper, lower, digit, symbol := false, false, false, false
			for _, r := range data.Password {
				switch {
				case r >= 'A' && r <= 'Z':
					upper = true
				case r >= 'a' && r <= 'z':
					lower = true
				case r >= '0' && r <= '9':
					digit = true
				default:
					symbol = true
				}
			}
			if !upper || !lower || !digit || !symbol || len(data.Password) < 32 {
				r := responseJSON(`{"error":{"code":400,"status":"INVALID_PASSWORD","message":"password lacks required complexity"}}`)
				r.StatusCode = 400
				return r, nil
			}
			key := req.URL.Path + "/" + data.Name
			if history[key] == nil {
				history[key] = map[string]bool{}
			}
			if history[key][data.Password] {
				r := responseJSON(`{"error":{"code":400,"status":"PASSWORD_REUSE","message":"password reuse was refused"}}`)
				r.StatusCode = 400
				return r, nil
			}
			history[key][data.Password] = true
		}
		return http.DefaultClient.Do(req)
	})
	p, err := newScoped(context.Background(), server, opts)
	require.NoError(t, err)
	defer func() { _ = p.Close() }()
	spec := goldenSpec()
	spec.Load = func(context.Context, secret.Value, secret.Value) error { return nil }
	g, err := p.RefreshGolden(context.Background(), spec)
	require.NoError(t, err)
	b, err := p.Branch(context.Background(), g.ID, "policy")
	require.NoError(t, err)
	raw, err := p.ConnString(context.Background(), b, provider.ConnDirect)
	require.NoError(t, err)
	require.NoError(t, reachable(raw.Reveal()))
}

func TestAnAdministratorCredentialChangedByAHookIsRestored(t *testing.T) {
	server := newFake(t, seedSQL)
	opts := options(t, server)
	target := ""
	opts.HTTPClient = cancellingTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/clone") {
			body, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			req.Body = io.NopCloser(strings.NewReader(string(body)))
			var data struct {
				CloneContext struct {
					Destination string `json:"destinationInstanceName"`
				}
			}
			if err := json.Unmarshal(body, &data); err != nil {
				return nil, err
			}
			target = data.CloneContext.Destination
		}
		return http.DefaultClient.Do(req)
	})
	p, err := newScoped(context.Background(), server, opts)
	require.NoError(t, err)
	defer func() { _ = p.Close() }()
	spec := goldenSpec()
	verify := spec.Verify
	spec.Verify = func(ctx context.Context, raw secret.Value) (string, error) {
		attestation, err := verify(ctx, raw)
		if err != nil {
			return "", err
		}
		parsed, err := url.Parse(raw.Reveal())
		if err != nil {
			return "", err
		}
		body, err := json.Marshal(map[string]string{"name": parsed.User.Username(), "password": "AF_FAKE_changed_Admin_password_1234!"})
		if err != nil {
			return "", err
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPut, server.URL()+"/v1/projects/"+testProject+"/instances/"+target+"/users?name="+url.QueryEscape(parsed.User.Username()), strings.NewReader(string(body)))
		if err != nil {
			return "", err
		}
		request.Header.Set("Authorization", "Bearer AF_FAKE_CLOUDSQL_TOKEN")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			return "", err
		}
		_ = response.Body.Close()
		if response.StatusCode != 200 {
			return "", fmt.Errorf("fixture password change refused")
		}
		return attestation, nil
	}
	g, err := p.RefreshGolden(context.Background(), spec)
	require.NoError(t, err)
	password, ok := server.PasswordOf(g.ProviderRef)
	require.True(t, ok)
	require.NotEqual(t, "AF_FAKE_changed_Admin_password_1234!", password)
}
