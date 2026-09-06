# fixed

The console's render boundary promised a reference that was not always there.

The boundary that catches a page which throws tells the reader that the
reference below is what to send us, and shows one only when the error carries
a digest. A throw with no digest left the sentence pointing at nothing. It
now says the short form in that case and keeps the longer one, with the
reference, for the errors that have one. Checked by rendering a page that
throws after mount in the built export, at 1440 and 390, in both states.
