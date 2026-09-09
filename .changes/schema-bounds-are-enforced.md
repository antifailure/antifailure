# fixed

The manifest's published schema declared 570 constraints and the engine kept
394 of them. `schemas/manifest.v1.json` is not an internal artifact: editors
validate against it, it is the document you learn the manifest from, and the
reference table on the site is generated out of it, so every bound in it reads
as a promise. 168 of those promises were kept by nothing. A `maxLength` of 40
on a service name accepted a name of any length, a `minimum` of 1 accepted
zero, an `enum` accepted a value not in it, and the only symptom would have
been an editor calling a manifest invalid that the engine ran, or the reverse.

The gap was invisible because every instrument near it answered a nearby
question. One test compares the field NAMES on the two sides and says in its
own comment that it deliberately compares nothing else. `manifestcheck` reads
the documentation's manifests for unknown keys. `fieldsweep` asks whether a
field is read at all. The validator's own comment stated the gap in one line
and read as a note rather than a defect.

The engine now enforces those bounds at parse time, driven by the published
schema itself rather than by a second hand written spelling of it, so the two
cannot disagree. Every hand written check stays: they carry better messages,
and they carry the cross field rules a JSON Schema cannot express, such as a
database declaring both a source and a seed. Where both would speak, the hand
written message wins, so one mistake is still reported once.
