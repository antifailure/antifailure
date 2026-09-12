# fixed

`af init` read any compose image whose name contained the letters `moto` as
the moto AWS emulator. An application running `acme/promotools` or
`motorola/firmware-sim` got AWS egress rules drafted from that container's
`SERVICES`, a wider firewall than the application needs, proposed by the
command whose job is to narrow it. moto is recognised now only under the two
repository names it publishes, `motoserver/moto` and
`ghcr.io/getmoto/motoserver`, with any tag, digest or registry in front. A
registry host that happens to contain `moto` no longer counts either.

The list form of a compose `environment` block, `- SERVICES=s3,sqs`, is now
covered by a test as well as the mapping form. It already worked, but nothing
checked it, so a regression would have looked exactly like an emulator that
named no services.
