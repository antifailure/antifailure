# fixed

The published retention numbers are now read from one place and compared to the
Terraform that sets them by a test, rather than kept in step by hand. Seven
legal claims were found false in one night and every one of them was true when
it was written, so the fix is a gate rather than seven edits.

The privacy and subprocessor pages no longer say there is no billing and that
nothing can send mail. Both were true before the billing and sign-in-link work
landed. They now separate what the software contains from what a deployment
switches on, because "we do not use Stripe" and "Stripe cannot be used" are
different promises and the pages were making the stronger one.
