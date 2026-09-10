package local

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/antifailure/antifailure/engine/pkg/airgap"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/require"
)

func TestEmulatorImagePullHonoursTheAirGapAndTheDaemonsAnswer(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		sealed bool
	}{
		{"missing image is refused while sealed", 404, true},
		{"cached image works while sealed", 200, true},
		{"an unanswered inspect is not a missing image", 500, true},
		{"an unsealed missing image is pulled", 404, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			airgap.Reset()
			t.Cleanup(airgap.Reset)
			if tc.sealed {
				airgap.Seal("the image pull test")
			}
			var present atomic.Bool
			present.Store(tc.status == 200)
			var pulls atomic.Int32
			daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if strings.HasSuffix(req.URL.Path, "/images/create") {
					pulls.Add(1)
					present.Store(true)
					_, _ = w.Write([]byte("{\"status\":\"done\"}\n"))
					return
				}
				if tc.status == 500 {
					w.WriteHeader(500)
					_, _ = w.Write([]byte(`{"message":"daemon unavailable"}`))
					return
				}
				if !present.Load() {
					w.WriteHeader(404)
					_, _ = w.Write([]byte(`{"message":"no such image"}`))
					return
				}
				_, _ = w.Write([]byte(`{"Id":"sha256:fixture"}`))
			}))
			defer daemon.Close()
			cli, err := client.NewClientWithOpts(client.WithHost(daemon.URL), client.WithVersion("1.44"))
			require.NoError(t, err)
			defer func() { _ = cli.Close() }()
			r := &Runtime{cli: cli}
			err = r.ensureImageByRef(context.Background(), "example/image:latest", "the test emulator", func(string) {})
			if !tc.sealed {
				require.NoError(t, err)
				require.EqualValues(t, 1, pulls.Load())
				return
			}
			require.Zero(t, pulls.Load(), "the sealed process asked Docker to fetch an image")
			switch tc.status {
			case 200:
				require.NoError(t, err)
			case 404:
				require.ErrorIs(t, err, airgap.ErrSealed)
			case 500:
				require.ErrorContains(t, err, "daemon unavailable")
			}
		})
	}
}
