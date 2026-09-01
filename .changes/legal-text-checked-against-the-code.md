# fixed

The published retention numbers are now read from one place and compared to the
Terraform that sets them by a test, rather than kept in step by hand. Seven
legal claims were found false in one night and every one of them was true when
it was written, so the fix is a gate rather than seven edits.

A data subject reading the retention page was told their data is gone after
fourteen days of point-in-time recovery. Production runs thirty-five.

The privacy and subprocessor pages no longer say there is no billing and that
nothing can send mail. Both were true before the billing and sign-in-link work
landed. They now separate what the software contains from what a deployment
switches on, because "we do not use Stripe" and "Stripe cannot be used" are
different promises and the pages were making the stronger one.

The retention page now says what happens when somebody asks to be removed: the
personal fields are erased and the account row is kept by choice, not because
the database refuses. Removing a provider key is revocation rather than
deletion, waitlist removal is carried out by hand, and operational log
retention is published for the first time.
