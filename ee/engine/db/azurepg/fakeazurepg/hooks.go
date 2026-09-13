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

// SetTagsForTest merges tags onto a server, so a test can put a server in a
// state a crash between restore and preparation would leave.
func SetTagsForTest(s *Server, name string, tags map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if srv, ok := s.servers[name]; ok {
		for k, v := range tags {
			srv.Tags[k] = v
		}
	}
}
