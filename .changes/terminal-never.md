# added

A terminal workflow can say what the program must never show. `never` takes
strings, matched character for character with case and spacing forgiven, and
one appearing fails the workflow even when every expectation was met:

```yaml
terminal_workflows:
  - name: deploy
    command: ./bin/deploy
    expect: ['"Applied 3 changes"']
    never: ["rollback started"]
    screen: {}
```

Without it, a full screen program that showed the expected words and went quiet
was accepted on that screen, so a program that went on to print an error was
passed on what it said first. That is still the default, because it is what
keeps a passing workflow fast. Declaring `never` keeps the program watched until
it exits or its budget is spent, ends the watch the moment a forbidden string
appears, and reports a watch the budget cut short with keys still to send as
blocked rather than passed. Every byte the program wrote is read as well as
every screen it drew, so a warning drawn and erased between two snapshots is
still caught. Set `budget.duration` to the window you mean. An entry that a quoted
expectation contains, or that a screen workflow types and so echoes, is refused
before anything runs, because either one decides the verdict by itself.

# fixed

A program that exited having written more output than its budget could draw
held a terminal workflow for as long as drawing it took, past the budget its
author declared: at the 46 to 150 KB a second measured on a loaded machine, a
20 MB exit held a thirty second workflow for minutes. The budget now bounds the
reading as well as the running, and a workflow that reaches it with output
undrawn is blocked, with the bytes written and how many were never drawn,
rather than judged on the part that was.
