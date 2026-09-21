# fixed

The pull request comment's chaos table said "Heap and index agree: yes"
whenever amcheck had answered anything, including when that answer was
"bt_index_check reported a problem" or that the amcheck extension was not
installed. The reviewer's page, read before merging, showed an index amcheck
had found broken as a clean one. The cell now says yes only when amcheck
verified the index. When it did not, the cell is bold and quotes amcheck's own
reason, and when the read back never finished, it says not checked.

The table also gains the row the terminal gained in `af chaos`: whether torn
pages in the writers' table were checked, claimed only when the control file
was read after the fault, data checksums are on and the read back finished.
The terminal and the comment now share one function for both answers, so they
cannot drift apart again.
