# changed

`SECURITY.md` said the antifailure.dev domain publishes no mail exchanger, and
gave that as the reason there is no security mailbox. The domain now has MX
records and receives mail, so the sentence had become false, and a false fact
in a security policy is worse than a missing one: it invites a reader to test
it and to distrust the rest of the page when it does not hold.

The policy itself is unchanged. Reports still go through a private
vulnerability report on GitHub, and there is still no security mailbox. What
changed is the reason given for it, which is now a choice that is stated
plainly rather than a limitation that no longer exists: no address on the
domain is monitored for security reports, and one sent there would miss every
commitment the page makes. The sender half of the old sentence was and remains
true, so it is kept and made specific: the root authorises no sender at all
under `v=spf1 -all` with DMARC `p=reject`.
