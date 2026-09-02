# fixed

The console's teardown button set a column and nothing anywhere read it.

`environments.teardown` marked the row `torn_down` and returned, with a comment
saying the engine holding the containers reads this and does the removing.
Nothing read it: not the engine, not a sweeper, not anything. The containers
kept running and the console said they were gone, which is worse than the button
not existing, because somebody who saw "torn down" stopped looking.

Teardown is now a durable request with a lease, an attempt count and an
acknowledgement. The environment's row moves only when the runtime confirms it:
the workflow run holding it reached a terminal state at GitHub, or the engine
reported the teardown itself. Cancelling that run is the only route this control
plane has into the machine holding the environment, and it is enough because
`af ci` tears down on every exit including a cancelled one.

Where there is no route at all, the request is given up on after its attempts
and says so, naming `af down`, rather than reporting a cleanup that never
happened.
