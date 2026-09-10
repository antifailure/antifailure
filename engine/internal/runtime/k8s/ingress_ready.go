package k8s

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/pkg/airgap"
)

// Pod readiness precedes ingress reconciliation. A ready pod can still have
// an ingress controller answering "no available server" for its published URL.
func waitForIngress(ctx context.Context, address, path string, timeout time.Duration) error {
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client := airgap.Client(airgap.SiteServiceProbe, 5*time.Second)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	defer client.CloseIdleConnections()
	last := "no response"
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(address, "/")+path, nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err == nil {
			_ = response.Body.Close()
			last = response.Status
			// These are the normal missing-route or missing-backend answers
			// of ingress controllers. Other application responses, including
			// authentication and application errors, establish reachability.
			switch response.StatusCode {
			case 404, 502, 503, 504:
			default:
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("published ingress did not become reachable (%s): %w", last, ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
}
