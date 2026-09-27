# added

The website editor now creates pages and Writing articles without a source
change. Each new path has an editable title, introduction, summary and rich
body, can contain image, video and code sections, and can be previewed before
publication. Publishing builds its URL, Markdown version and sitemap entry;
articles also appear in the Writing index and RSS feed. Existing articles are
listed individually for editing.

Ask AI now starts with the full page, can align label icons with
their adjacent copy on desktop, and uses the stronger design model for layout
requests while short copy edits stay on the smaller model. Every suggestion
still requires review before it enters a draft.
It can also propose a new page or article as a private, editable draft, with
route collision checks and a publish gate for incomplete content.
The network gate's denied-probe test now keeps its port allocated on one
loopback address and probes another, so a new positive-control listener cannot
accidentally claim the supposedly denied endpoint during a busy CI run.
