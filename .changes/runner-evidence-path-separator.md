# fixed

On Windows the path `af test` printed for each workflow's trace and screenshot
used two separators at once, `C:\work\app\.antifailure\artifacts\env/flow-1.trace.zip`,
because the runner glued a forward slash onto the native artifacts directory
the engine handed it. Windows opened it anyway, but it was a path in two styles
for somebody to copy. It is joined with the platform's own separator now.
