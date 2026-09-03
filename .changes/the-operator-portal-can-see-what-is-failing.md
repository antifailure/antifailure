# added

The operator portal has its Operations sections: infrastructure, failures, email
and the kill switches.

Infrastructure and Compute and Incidents and Kill Switches are faces for routes
that already existed and had none: system health, the fleet of production twins,
the teardown ledger, the egress firewall, and the three switches that stop parts
of an installation. The switches show what each one refuses AND what keeps
working, require a reason to engage, make the operator type the control's own
name, and show the way back at all times. A fleet teardown asks the server what
it would touch and makes that count the thing you type, so nobody confirms a
blast radius they have not read.

Logs and Error Explorer and Email and Notifications are new, and both are built
only from what this product records. There is no exception store, so failures
are grouped by the failure code a run ended with, and by the workflow a verdict
names. There is no delivery record, so the email page reports whether the
installation can send at all, which no query over the database can answer, and
counts sign-in links by what became of them. A link issued and never used before
it expired is the only trace a delivery failure leaves anywhere in this schema,
and the page says that rather than calling the column a bounce.

Both pages name what they cannot show and what it would take to change it. Event
payloads are never returned: the field names and the byte size say whether
ingestion is working without putting a copy of a tenant's data in an operator's
browser.
