# fixed

The cross platform lint refused `internal/mcp/paths_test.go` because
`syscall.Mkfifo` does not exist on Windows. The test carried a
`runtime.GOOS == "windows"` skip, which reads as though the platform had been
handled and cannot help: the skip runs at run time and the missing symbol is a
compile error, so the whole package failed to typecheck.

The fifo test moves to `paths_fifo_unix_test.go` behind `//go:build !windows`,
matching how the package already separates `open_unix.go` from
`open_windows.go`. It still runs on every platform that has `mkfifo`.
