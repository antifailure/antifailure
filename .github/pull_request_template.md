## What changed

<!-- One paragraph. What a reader of the changelog needs to know. -->

## Failure paths covered

<!--
List each failure path this change handles and the test that proves it.
One per line, as "path: TestName". A change with no failure paths says so
and explains why (for example, documentation only).
-->

| Failure path | Test |
| --- | --- |
|  |  |

## Security considerations

<!--
Answer all four, or write "none, and here is why".
- Does this touch secrets, masked data, the proxy, or the journal?
- Does it add a new outbound connection or a new stored data class?
- Does it change what an untrusted repository or pull request can reach?
- Does it add a dependency? If so, what license?
-->

## Exit criteria evidence

<!--
Paste actual command output, not a paraphrase. At minimum "just gate".
-->

```
$ just gate

```

## Checklist

- [ ] Commits are signed off (`git commit -s`)
- [ ] A changelog fragment exists under `.changes/`
- [ ] Documentation is updated for anything user visible
- [ ] Every new error code has a catalog entry
- [ ] No secret, real customer record, or unredacted screenshot is included
