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

// ReleaseRestorePointsForTest makes every server that has no backup yet
// restorable now, as Azure's first snapshot finishing would. With it a test
// holds a golden unrestorable for as long as it likes and ends the wait at the
// moment it chooses, instead of racing a short FirstBackupDelay against however
// long the steps before the restore happened to take on that machine.
func ReleaseRestorePointsForTest(s *Server) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.opts.Now().UTC()
	for _, srv := range s.servers {
		if srv.EarliestRestore.After(now) {
			srv.EarliestRestore = now
		}
	}
}
