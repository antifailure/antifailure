//go:build windows

package replay

// syncDir does nothing on Windows, because there is nothing it could do. A
// directory opened the way os.Open opens one cannot be flushed: Windows answers
// FlushFileBuffers on it with "Access is denied", which is what failed every
// replay and incident write on Windows with AF-RPL-001. NTFS records a rename
// or a new link in its own journal rather than in the directory's data, so
// there is no directory content left to flush. The file itself is still
// synced before it is renamed into place.
func syncDir(string) error { return nil }
