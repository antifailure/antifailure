# fixed

Two holes in the golden lifecycle, and both of them were on the path most runs
actually take.

`database.extensions` was enforced when a golden was built and never when one
was branched. A project builds its first golden once and branches it for the
rest of its life, and the golden's identity does not depend on the extension
list, so the ordinary sequence was: the golden exists, a branch adds `postgis`
to the manifest beside the migration that needs it, `af up` selects the golden
it already had, and the environment came up green with the extension simply
absent. `SELECT count(*) FROM pg_extension WHERE extname = 'postgis'` answered
0 and nothing anywhere said so, while the same manifest on a project with no
golden yet was correctly refused with AF-DB-040. A refusal that fires on the
run nobody takes twice, and never on the one everybody takes, is close to no
refusal. Every branch now creates what the manifest declares, so an extension
the golden predates is created on the branch and an extension the image cannot
carry is refused by name, with the image the manifest asked for named beside
it, on both paths.

`af golden refresh` never ran `database.seed`. A project whose only data comes
from that command got a golden holding no tables, a report of "0 rows across 0
tables masked", the words "Verified 0 columns across 0 tables", exit 0, and an
invitation to bring an environment up from it. That golden's provenance is the
project's own, so the next `af up` selected it and branched an empty database.
The seed command was validated, refused alongside `database.source_url_env`,
printed by `af explain`, and executed by nothing. Both paths that publish a
golden now run it, through the same call.

AF-DB-041 is the second guard, and it is deliberately not the same fix twice.
Running the seed closes the one route that produced this. A seed that exits 0
having written nothing, or a source database that turns out to be empty, reach
the same published, signed, empty golden by other routes, and the word the
product asks people to rely on is verified. A golden that holds no tables at
all is now refused rather than attested, when the manifest declares where its
contents come from. A project that declares neither `database.source_url_env`
nor `database.seed` is untouched: an empty golden is what it asked for and
what it is documented to get.
