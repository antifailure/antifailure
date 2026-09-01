# changed

`af workload` is hidden from `af --help`. The commands a person runs are
`af load run`, `af load scenario`, `af test` and `af explore`; this is what a
hosted control plane calls on their behalf, and a top level command
introducing a noun that appears nowhere else in the product would be one more
word to learn for something nobody types. Its flags are documented under
Workloads instead.

The documented `safe_routes` and `unsafe_routes` examples now use `**` rather
than `*`. A single star covers exactly one path segment, so `DELETE /*` blocks
`DELETE /orders` and not `DELETE /orders/42`, and a delete almost always
carries an id. Under a safe list permissive across methods, that made the entry
somebody copies to stop deletes send the realistic ones and say nothing. The
matcher is unchanged, because making a single star span segments would change
what every deployed manifest already means.
