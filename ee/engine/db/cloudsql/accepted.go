// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package cloudsql

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// A successful status proves the cloud accepted the mutation even if its
// response body cannot be read. A transport error proves neither acceptance
// nor refusal; callers must not delete an unowned name on that evidence.
type acceptedResponseError struct{ error }
type uncertainResponseError struct{ error }

func (e *acceptedResponseError) Unwrap() error  { return e.error }
func (e *uncertainResponseError) Unwrap() error { return e.error }

func acceptedResponse(err error) bool {
	var accepted *acceptedResponseError
	return errors.As(err, &accepted)
}

func uncertainResponse(err error) bool {
	var uncertain *uncertainResponseError
	return errors.As(err, &uncertain)
}

func (p *Provider) removeCreated(ctx context.Context, name string) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
	defer cancel()
	op, err := p.api.deleteInstance(cleanup, name)
	if notFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cloudsql: cleanup of accepted instance %q failed: %w", name, err)
	}
	if err := p.api.waitForOperation(cleanup, op, p.opts.PollInterval); err != nil {
		return err
	}
	if _, err := p.api.getInstance(cleanup, name); notFound(err) {
		return nil
	} else if err != nil {
		return err
	}
	return fmt.Errorf("cloudsql: accepted instance %q still exists after cleanup", name)
}
