package dockerutil_test

// The distinction this file exists for is which ERROR came back, and a real
// daemon cannot be asked to produce a chosen error on demand. So the inspector
// is a fake, and the three cases are the three answers a daemon gives: the
// image is here, the image is not here, and I could not tell you.

import (
	"context"
	"errors"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
)

type fakeInspector struct {
	resp image.InspectResponse
	err  error
	refs []string
}

func (f *fakeInspector) ImageInspect(
	_ context.Context, ref string, _ ...client.ImageInspectOption,
) (image.InspectResponse, error) {
	f.refs = append(f.refs, ref)
	return f.resp, f.err
}

func TestImagePresent_APresentImageIsPresent(t *testing.T) {
	t.Parallel()
	f := &fakeInspector{resp: image.InspectResponse{ID: "sha256:abc"}}
	got, err := dockerutil.ImagePresent(context.Background(), f, "antifailure/proxy:tag")
	require.NoError(t, err)
	require.True(t, got)
	require.Equal(t, []string{"antifailure/proxy:tag"}, f.refs)
}

func TestImagePresent_ANotFoundIsTheOnlyThingReadAsAbsence(t *testing.T) {
	t.Parallel()
	f := &fakeInspector{err: cerrdefs.ErrNotFound.WithMessage("No such image: antifailure/proxy:tag")}
	got, err := dockerutil.ImagePresent(context.Background(), f, "antifailure/proxy:tag")
	require.NoError(t, err, "a not found is the daemon answering, not failing")
	require.False(t, got)
}

// The case that produced this helper.
//
// A busy daemon returns something that is not a not found, and the inline
// check it replaces read every error as absence. Reported as absent, this
// sends a caller on to do the thing it does when an image is missing, having
// been told nothing at all. The requirement is that the error comes back.
func TestImagePresent_ABusyDaemonIsNotAnAbsentImage(t *testing.T) {
	t.Parallel()
	busy := errors.New("Client.Timeout exceeded while awaiting headers")
	f := &fakeInspector{err: busy}
	got, err := dockerutil.ImagePresent(context.Background(), f, "antifailure/proxy:tag")
	require.Error(t, err,
		"a daemon that did not answer must not be reported as an absent image")
	require.ErrorIs(t, err, busy, "the daemon's own error has to survive to the caller")
	require.False(t, got)
	require.Contains(t, err.Error(), "antifailure/proxy:tag",
		"the message must name the image, because the caller asked about one")
}

// A daemon error that is not a not found must not be READ as one just because
// it mentions the words. IsNotFound is a type question, not a string question.
func TestImagePresent_AnErrorThatMerelyMentionsNotFoundIsStillAnError(t *testing.T) {
	t.Parallel()
	f := &fakeInspector{err: errors.New("registry not found in daemon configuration")}
	_, err := dockerutil.ImagePresent(context.Background(), f, "antifailure/proxy:tag")
	require.Error(t, err)
}
