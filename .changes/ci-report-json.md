# added

`af ci --report-json <path>` writes the same run as JSON, for something that
has to act on the report rather than display it. `--report` stays Markdown for
a person.

`-o json` is not that flag. It is the whole terminal's format, so a continuous
integration step that shows progress to somebody watching the job and also
captures a machine readable result would have to give up one or the other, and
redirecting stdout to a file gives up the progress.

The worked example for `af ci` wrote its Markdown report to a file called
`report.json`, which read as though `--report` produced JSON. It now names both
flags and both file types.
