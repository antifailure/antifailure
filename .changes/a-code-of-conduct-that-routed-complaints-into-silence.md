# fixed

The code of conduct no longer names an address that cannot receive a
harassment report.

CODE_OF_CONDUCT.md named conduct@antifailure.dev as the place to report abusive
behaviour. The antifailure.dev domain publishes no mail exchanger, an SPF
policy authorising no sender, a DMARC policy of reject with strict alignment,
and a DKIM record whose empty `p=` revokes the key. Nothing addressed there was
ever delivered, and the person reporting got a silence they could not tell
apart from being ignored.

There is no confidential channel to put in its place, so the section says that
instead of naming another mailbox. It lists what does resolve, a public issue
or discussion and a booked call with the maintainer, and what each one costs
the reporter. It says that GitHub private vulnerability reporting is the wrong
queue for this. It names the gap none of them closes: whoever would read a
complaint is one of the same few people who maintain the repository, so a
complaint about a maintainer has nowhere independent to go inside the project,
and it points at GitHub's own abuse route, which is outside it.

The daily status workflow committed as status@antifailure.dev, which reads like
a mailbox and is not one. It now commits under GitHub's own no-reply identity
for the Actions bot.

# added

A gate that refuses a contact route this project cannot answer.

The site had already been fixed and the repository had not, which is why this
exists rather than a third correction. `tools/contactcheck` reads every text
file in the tree and requires a row in `tools/docs/contact-routes.tsv` for
every address, saying whether a person reads it, whether it is a value the
software writes rather than an invitation, or whether it is a defect being
quoted. An address with no row fails, so a domain nobody has checked is caught
by the missing row. A `receives` row at a domain proven dead is refused
outright, and at such a domain no sentence anywhere in the tree may read as an
instruction to write there, whatever its row says.

It does not resolve MX at check time. That would make every pull request depend
on somebody else's resolver, and it answers the wrong question: an MX record
proves a server accepts mail, not that a person reads what lands there. What it
cannot catch is written into the tool's own header, including the case it is
blindest to, which is a second domain vouched for by a row nobody verified.
