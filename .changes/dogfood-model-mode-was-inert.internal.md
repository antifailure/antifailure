# fixed

`dogfood.yml` set `AF_MODEL_MODE=replay` in two jobs and called it a spend
control. Nothing has ever read that variable, the ones the runner reads are
`AF_MODEL_CASSETTE` and `AF_MODEL_CASSETTE_MODE`, no recording exists at any
revision to replay, and the nightly smoke the comment said already spends money
on a model is not in this repository. The variable is gone and the comment now
says what is true: no job here sets a model key, so the agents run on the
deterministic planner and the spend is zero because there is nothing to spend
it with.
