# fixed

The sweep's in-use error now wraps the lock error as well as the sentinel, so
the reason an environment was deferred survives to the caller.

`fmt.Errorf("%w: %v", ErrInUse, err)` formatted the lock failure as text, which
errorlint refuses and which threw away the only description of what was holding
the environment. Both are wrapped now. `errors.Is(out.Err, env.ErrInUse)` in
the CLI, the one consumer, is unaffected.
