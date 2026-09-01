package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// maxRepositoryFileBytes bounds a file this server will read from the
// candidate checkout.
//
// A migration is a few kilobytes. A cap two orders of magnitude above that is
// generous for anything legitimate and still refuses a caller that points the
// server at a multi gigabyte file to make it allocate.
const maxRepositoryFileBytes = 4 << 20

// CheckedFile is a file that was resolved inside the checkout and read.
//
// The bytes and the hash come from a single open handle, so what was validated
// and what was hashed are the same inode. That is the point of the type: a
// path that is checked and then reopened by name later has not been checked at
// all, because the name can be pointed somewhere else in between.
type CheckedFile struct {
	// Rel is the path relative to the checkout root, cleaned. It is what a
	// report refers to, because an absolute path names the host's filesystem
	// and a report has no business doing that.
	Rel string
	// SHA256 is the hash of the exact bytes read.
	SHA256 string
	// Bytes is the content, bounded by maxRepositoryFileBytes.
	//
	// It is CANDIDATE CONTENT AND UNTRUSTED. A migration is data written by
	// whoever opened the pull request, so it may contain text addressed at a
	// model reading this output. Nothing in this package renders it into a
	// report, and nothing should: a report says a statement failed and which
	// one, never what the statement said.
	Bytes []byte
	// Size is the number of bytes read.
	Size int64
}

// resolveInRoot resolves a caller supplied relative path inside a checkout.
//
// Every one of these checks has a specific attack behind it and none of them
// is redundant.
//
// The path is refused outright if it is absolute or contains a parent
// reference, before any filesystem call, because a path that never reaches the
// filesystem cannot race anything.
//
// The root and the target are both resolved through symlinks and compared on a
// separator boundary. Comparing resolved paths is what catches a symlink
// inside the tree pointing out of it, which a textual check on the unresolved
// path cannot see. Comparing on a separator boundary rather than by prefix is
// what stops a sibling directory whose name merely starts with the root's from
// passing.
//
// The handle is opened first and inspected afterwards through the handle, not
// the name. Opening a fifo would block forever, so it is opened without
// blocking and then refused if it turns out not to be a regular file, which
// rejects devices, sockets, directories and pipes in one check.
func resolveInRoot(root, rel string) (*CheckedFile, *Fault) {
	if rel == "" {
		return nil, fieldFault(FaultPathRejected, "repository_file",
			"This field must name a file.")
	}
	if len(rel) > 1024 {
		return nil, fieldFault(FaultArgumentTooLarge, "repository_file",
			"This path is longer than any this server accepts.")
	}
	// A NUL byte truncates a path at the system call boundary, so a name
	// carrying one means something different to the checks above than it does
	// to the kernel.
	if strings.ContainsRune(rel, 0) {
		return nil, fieldFault(FaultPathRejected, "repository_file",
			"This path contains a null byte.")
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, `\`) {
		return nil, fieldFault(FaultPathRejected, "repository_file",
			"This path must be relative to the repository root.")
	}
	// Volume names are refused explicitly rather than left to IsAbs, because
	// a Windows style "C:file" is relative to a drive's current directory and
	// is not caught by IsAbs.
	if filepath.VolumeName(rel) != "" {
		return nil, fieldFault(FaultPathRejected, "repository_file",
			"This path must be relative to the repository root.")
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return nil, fieldFault(FaultPathRejected, "repository_file",
			"This path leaves the repository.")
	}

	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		// The checkout itself is unreadable, which is a server side problem
		// and not the caller's fault.
		return nil, &Fault{
			Code:   FaultSafetyUnavailable,
			Detail: "The repository checkout this server was started against cannot be read.",
			// The host path travels in the log, never in the result.
			wrapped: err,
		}
	}
	target := filepath.Join(realRoot, clean)

	// Resolved before opening, so that a symlink pointing outside the tree is
	// refused rather than opened. The open below re-checks the result through
	// the handle, which is what closes the window between the two.
	realTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fieldFault(FaultPathRejected, "repository_file",
				"No such file in the repository.")
		}
		return nil, fieldFault(FaultPathRejected, "repository_file",
			"This path cannot be resolved inside the repository.")
	}
	if !within(realRoot, realTarget) {
		// The message does not say where it pointed. A caller that aimed a
		// symlink out of the tree does not need the resolved host path
		// confirmed back to it.
		return nil, fieldFault(FaultPathRejected, "repository_file",
			"This path resolves outside the repository.")
	}

	// O_NONBLOCK so that a fifo does not hang the server on open, which is
	// what a plain Open on a named pipe with no writer does. It is refused a
	// few lines below for not being a regular file; the flag is only there so
	// that the refusal is reachable.
	f, err := os.OpenFile(realTarget, os.O_RDONLY|syscallNonblock, 0)
	if err != nil {
		return nil, fieldFault(FaultPathRejected, "repository_file",
			"This file cannot be opened.")
	}
	defer func() { _ = f.Close() }()

	// Through the handle, not the name. Between EvalSymlinks and Open the
	// name could have been repointed; the handle cannot be, so this is the
	// check that decides.
	info, err := f.Stat()
	if err != nil {
		return nil, fieldFault(FaultPathRejected, "repository_file",
			"This file cannot be inspected.")
	}
	if !info.Mode().IsRegular() {
		return nil, fieldFault(FaultPathRejected, "repository_file",
			"This path is not a regular file.")
	}
	if info.Size() > maxRepositoryFileBytes {
		return nil, fieldFault(FaultArgumentTooLarge, "repository_file",
			"This file is %d bytes, and this server reads at most %d.",
			info.Size(), maxRepositoryFileBytes)
	}

	// One byte past the cap, so that a file which grew between the stat and
	// the read is refused rather than silently half read.
	body, err := io.ReadAll(io.LimitReader(f, maxRepositoryFileBytes+1))
	if err != nil {
		return nil, fieldFault(FaultPathRejected, "repository_file",
			"This file cannot be read.")
	}
	if len(body) > maxRepositoryFileBytes {
		return nil, fieldFault(FaultArgumentTooLarge, "repository_file",
			"This file is larger than the %d bytes this server reads.",
			maxRepositoryFileBytes)
	}

	sum := sha256.Sum256(body)
	return &CheckedFile{
		Rel:    filepath.ToSlash(clean),
		SHA256: hex.EncodeToString(sum[:]),
		Bytes:  body,
		Size:   int64(len(body)),
	}, nil
}

// within reports whether path is root or sits underneath it.
//
// The separator is appended before the prefix test, because "/repo" is a
// prefix of "/repo-backup" as a string and is not its parent as a path. That
// is the whole bug this function exists to not have.
func within(root, path string) bool {
	if path == root {
		return true
	}
	if !strings.HasSuffix(root, string(filepath.Separator)) {
		root += string(filepath.Separator)
	}
	return strings.HasPrefix(path, root)
}
