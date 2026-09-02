# fixed

`load.safe_routes` and `load.unsafe_routes` entries carrying an HTTP method
matched nothing. Normalisation prefixed anything not starting with a slash with
one, so `DELETE /*` became `/DELETE /*`, and the matcher compares the method
exactly. Every example in the load documentation is written that way, so every
one of them was inert.

The safe list failing like this is loud: a run that may send nothing refuses
everything and says so. The unsafe list failing like this is silent, and that
is the reason this matters. A list that matches nothing refuses nothing, so a
manifest with a permissive safe list and `unsafe_routes: ["DELETE /**"]` sent
the deletes the author wrote that entry to prevent, at production's rate.

A method the matcher would compare is now kept and upper cased. A path carrying
a space is not a method and stays whole.
