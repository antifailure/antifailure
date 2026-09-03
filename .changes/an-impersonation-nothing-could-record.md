# added

The operator portal can answer a customer's question from their side of the
product, and write down what it found.

Three sections behind Customers. Users & Organizations lists every organization
and every account, with the sessions one account holds and the two writes that
end them. Support & Impersonation keeps operator notes about a customer and is
where an operator steps into an account. Billing & Stripe reads a customer's
subscription, invoices, charges and credit from the payment provider rather
than from a local copy of it, beside this deployment's own record of every
administrative money action taken on the account.

Impersonation is bounded rather than trusted. It needs a stated reason of at
least eight characters, it lasts minutes rather than the product's thirty days,
the operator portal is closed to that session for as long as it lasts, and the
customer gets the record in their own audit log at the moment it starts. The
audit entry is written before the session exists, structurally: the session row
carries the entry's sequence number as a foreign key into the chain, so a
session that was never recorded cannot be represented.

Two ways out, and both now work. Ending it revokes the customer session the
browser is holding, and so does signing out of the operator portal, which is
the only button the portal offers a session that is already inside one.
