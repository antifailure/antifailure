# fixed

`af down --help` said it deletes "every resource the environment created" and
then named four kinds. It removes what the journal recorded, which is not the
same set, and anything this build has no way to delete is left recorded and
reported rather than removed. The help says that now, in a form that cannot
fall behind the code, and points at `af status` and the pending list for the
answer rather than at a sentence.

`plan_regression` is described as three ways a plan can get worse rather than
two. `cost_increase` was named nowhere a user could read: not in the manifest
schema, not in the generated reference, not in the verdicts guide.
