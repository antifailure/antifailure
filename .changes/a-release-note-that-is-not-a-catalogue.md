# changed

The changelog page and the release notes are something you can scan rather than
something you have to sit down to.

`/changelog` rendered every entry open on one list, and v1.0.0 carries a bit
over two hundred public entries, so the page stood 136,766 pixels tall at 1440
and 264,772 at 320. That is 152 screens of scrolling on a desktop and 259 on a
phone, with one entry fully on screen in the whole of it. Entries are grouped
by category now, each is a single line headed by its author's own opening
sentence, and opening one is a `details` element. The same page is 19,635
pixels at 1440 and 26,386 at 320, with eight entries fully on a screen of the
list and twenty two screens to the bottom. A release states its size, the days
its work landed between, and how many entries of each kind it holds, as four
links that scroll to them. The search reads every word of every entry,
including the ones that are collapsed, because a collapsed entry is in the page
rather than absent from it. With JavaScript off the whole changelog is still
there and a `details` still opens.

The release notes were the entire v1.0.0 section of `CHANGELOG.md`, 69,932
bytes of it, which is under GitHub's limit and far past what anybody reads. A
section can mark part of itself as detail now, between `<!-- relnotes:omit -->`
and `<!-- relnotes:end -->`, and the published notes carry a link to the
changelog where that part stood. The verification commands, what 1.0 promises
and what it does not, what pushing the tag moves, how to install it, everything
that behaves differently under an existing manifest, and every security entry
are all still in the body, which is 23,170 bytes. `CHANGELOG.md` keeps all of
it, because a changelog file is a reference document and people search it.

`just relnotes` grew with the feature rather than being loosened by it. It
refuses an unbalanced marker, a second region in one section, an empty region,
and a section that omits every line of itself, and its emptiness check reads
what a tag would publish rather than what is in the file.

# fixed

Two shapes of markdown reached the changelog page as their own punctuation.
Bold around a run containing inline code printed the backticks, and a word
between single asterisks printed the asterisks: four backticks and two pairs of
asterisks, on a page a prospective customer reads. Bold and italic carry spans
now rather than text, so what is inside them is rendered rather than shown.
