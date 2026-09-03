# fixed

The retention page now says what happens when somebody asks to be removed: the
personal fields are erased and the account row is kept by choice, not because
the database refuses. The delete would succeed. What it would also do is null a
column that sits inside the audit hash chain, so every entry that person wrote
would stop hashing to its recorded hash and the organization's audit log would
report itself as altered.
