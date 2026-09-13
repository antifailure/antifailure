# fixed

Provisioning a persona read every user in the golden to find one. The SQL
adapter addressed a row by its key cast to text, and any key that is not
already text becomes an expression no index can be searched by, so reconciling
a persona's account, its Supabase identity and its second factor each read the
whole table or the whole index. Supabase's uuid keys and a generic scheme's
integer keys were all affected; a text key was not. Against a million Supabase users the users update took a median of
185 ms and each identity lookup up to 126 ms, per persona and on every branch,
where the same statements searching the index take a few milliseconds or less.
The key is now compared as its own type, which matches the same row for
integer, uuid, text and character keys and lets Postgres use the index.
