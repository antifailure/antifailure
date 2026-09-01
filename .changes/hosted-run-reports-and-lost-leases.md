# fixed

A hosted load run recorded as having sent no requests at all, and a run whose
engine lost it could be ended by that engine.

The control plane read the run-wide aggregate of a report using the field names
of the engine's internal load result rather than the names of the result
document the engine actually sends. Two of the four workload kinds were
affected, and it failed silently in the worst available direction: the request
count falls back to zero rather than refusing, because the column requires one,
so a run that sent twelve hundred requests recorded as having sent none with
every percentile empty, while every route, threshold and piece of evidence
beside it read perfectly. A console draws that as a strange run rather than as a
broken reader. It survived because the engine's tests checked a document the
engine wrote itself and the control plane's tests checked a document the control
plane wrote itself, so the wire between them had never carried a real message.
It is now read from documents a real engine produced, one per kind, and the
check fails in both directions: a name the reader looks for that no engine
sends, and a number an engine sends that arrives as something else.

Separately, an engine holds a run under a lease and extends it by heartbeat.
Miss enough heartbeats and a second engine may claim the run and start doing the
work. The statement that ends a run asked only whether the run was still open,
not who was holding it, so the first engine's final event ended the run and the
second engine's report then arrived against a closed run and was kept only as a
note. The measurements of the engine that did the work were lost. A final event
is now accepted only from the engine that holds the run, or from any engine when
nothing holds it, which is the ordinary case for a run started by hand.

A run that changed hands and a run whose only engine died used to read
identically once they timed out: both said nobody reported. A run now records
when its lease was taken by another engine and when an engine that no longer
holds it tried to end it, and says which of the four it was in its own words.
