// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package fakeazurepg

import "time"

// SetFirstBackupDelayForTest sets how long a server this fake restores waits
// before it has a backup to restore from. It exists for the provider's own
// suite, which builds the fake through a shared helper.
func SetFirstBackupDelayForTest(s *Server, delay time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.opts.FirstBackupDelay = delay
}
