# fixed

Database snapshots retain each occurrence of a row in a table without a primary key. Adding an identical row is now visible to the comparison, and repeated rows count toward the capture limit. This also prevents an agent replay from treating a duplicate insert as an unchanged database.
