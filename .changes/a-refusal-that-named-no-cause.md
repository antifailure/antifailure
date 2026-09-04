# fixed

The npm advisory gate refused and would not say why.

`npm advisories` went red on main and on every open pull request, and the whole
output was `npmaudit: api: npm audit refused:` with nothing after the colon. npm
had returned a report whose `error` object was present and whose code, summary
and detail were all empty, and the format string printed the three empty strings
faithfully. The step had run for exactly five minutes first, twice, which is
npm's own default fetch timeout and the shape of a registry that never answered.
None of that reached anybody, because this is the one error path that fires when
the registry is unreachable and it was the one path of the three that did not
carry stderr.

A refusal now carries npm's exit error and its stderr, and an empty refusal
reports itself as empty rather than trailing off after a colon. An empty stderr
is stated out loud, because npm printing nothing and this tool discarding what
npm printed look identical in a log, and the second of those is what was
happening.
