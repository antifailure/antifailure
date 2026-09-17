# added

Enterprise and demo requests were recorded and no operator could see them.

The "talk to us" form writes a row into enterprise_leads, and migration 0035
arranged that table so the anonymous public endpoint can only INSERT and can
never read a row back: a serving role that could also SELECT there is one query
bug away from publishing every prospect's name, company and message. That
boundary was right, and the reader it left was wrong. The only way to see a lead
was af-control-plane-backup leads, a command that needs the migration role or
the cluster superuser, so in practice a lead landed and nobody with the operator
portal open could read it. It was the waitlist failure the table was built to
end, one step downstream: a form that consumed what somebody typed.

The operator portal now has an Enterprise Leads page under Administration. It
reads through the operator pool, whose role already held SELECT on the table by
0023's default privileges, and a one line migration grants that same role UPDATE
so a lead can be marked handled. The serving role is untouched and stays
INSERT-only, so the leak boundary is exactly as 0035 drew it. The queue is
oldest first, the message opens in a panel rather than a cell, and marking a
lead handled records who did it and when so nobody is answered twice or never.
It mails nobody, and the page says so.
