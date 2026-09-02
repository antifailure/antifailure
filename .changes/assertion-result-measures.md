# changed

A scenario assertion now reports what it measured as well as what it concluded.
`af load scenario -o json` carries `measure`, `scope`, `threshold` and
`observed` on every assertion result beside the sentence it already printed. The
sentence is for a person; a dashboard cannot chart "served a p95 of 240ms, over
200ms" without parsing English, and cannot tell that measure from another one.
An assertion whose requests were never sent reports no observation at all rather
than zero, because zero reads as a perfect application and means a question
nobody asked.
