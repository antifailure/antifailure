# fixed

The enterprise compliance report called every real masking attestation forged.
Each golden's attestation showed "SIGNATURE DOES NOT VERIFY" in the SOC 2 and
HIPAA evidence, although nothing had been changed.

An attestation is signed over its own fields, and the compliance report checks
the signature by rebuilding those fields and hashing them again. Since
2026-09-01, before 1.0, the engine has signed four fields the compliance report
did not know about: the project a golden was made for, which datastore the scan
read, the columns the scanner could not read, and the columns masking copied
unchanged because no rule named them. The report dropped all four when it
rebuilt the document, hashed different bytes, and failed every signature. Its
own tests passed, because they checked attestations written before those fields
existed.

The report now reads every field the engine signs, and real attestations
verify. The engine's test suite signs an attestation with every field set and a
fixed key, and the compliance suite must verify that same file, so a field added
to one side without the other now fails a test rather than a customer's audit
evidence.
