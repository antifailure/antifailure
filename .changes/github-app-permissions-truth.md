# fixed

The production runbook prescribed a GitHub App permission and four webhook
events that nothing in this repository uses. Checks read and write is gone,
because no code calls the Checks API. Pull request, Push, Member and Membership
are gone from the subscription list, because the webhook handler answers
`handled: false` for all four and an event nobody consumes only makes a real
failed delivery harder to find. Actions read and write is added, with what needs
it and why to grant it when the App is created rather than later. The page is
also unambiguous that Device Flow stays off, and says what it would be for.
