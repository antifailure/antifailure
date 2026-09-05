# fixed

An expiry sweep removed its target but reported lifecycle events under the
checkout's environment name. The events now name the environment the sweep
actually removed, so it cannot report another branch as torn down.
