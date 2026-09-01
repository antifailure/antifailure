# fixed

`examples/django-api` and `examples/next-app` declared no workflows at all.
Each reported "Antifailure: Nothing ran" while the check went green, because a
run that never reaches an agent still exits zero. Both now carry a workflow
that reads the page the application exists to serve, and both have been run
against a real browser over a real branched database rather than written and
assumed.

`a-viewer-cannot-edit-policy`, in this repository's own manifest, expected two
strings the owner also sees. A viewer who could propose a rule and approve
every pending one would have passed a workflow named for not being able to. It
names the sentence the console shows exactly when the propose form and the
approve column are withheld, so the two network workflows now fail when driven
by each other's persona.
