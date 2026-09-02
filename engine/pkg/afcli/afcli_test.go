package afcli_test

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/afcli"
)

// The enterprise binary gets the second control C too, and this is the hop it
// depends on.
//
// Both binaries discarded the second signal, and the comment above the
// enterprise main said it had "the same signal handling the community binary
// has, from the same function, so that control C means the same thing in
// both". It did, in the sense that neither of them had it. The community side
// is tested where Execute lives; the enterprise side reaches that code only if
// this package carries Forced across, so a pass-through that quietly went
// missing would put the enterprise binary back exactly where it was with
// nothing going red.
func TestRun_CarriesTheSecondInterruptThrough(t *testing.T) {
	release := make(chan struct{})
	returned := make(chan struct{})
	t.Cleanup(func() {
		close(release)
		<-returned
	})

	forced := make(chan struct{})
	close(forced)

	code := make(chan int, 1)
	go func() {
		code <- afcli.Run(context.Background(), forced, []string{"hang"}, afcli.Options{
			Stdout: io.Discard, Stderr: io.Discard,
			Getenv: func(string) string { return "" },
			Extra: []afcli.Command{{
				Use:   "hang",
				Short: "Block until the test says otherwise",
				Run: func(context.Context, []string) int {
					defer close(returned)
					<-release
					return 0
				},
			}},
		})
	}()

	select {
	case got := <-code:
		// 10 by its number rather than by the internal constant, because this
		// package is the public edge and the number is what a script reads.
		require.Equal(t, 10, got,
			"a forced stop has to exit 10, which the error reference documents as interrupted with resources still recorded")
	case <-time.After(10 * time.Second):
		t.Fatal("a second interrupt did not stop the run through afcli, so the enterprise binary still ignores it")
	}
}
