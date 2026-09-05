# added

A gate that refuses to publish the marketing site when it calls a control plane
route the deployed control plane does not serve.

Somebody filled in the careers form on antifailure.dev and was told "Could not
reach the server". Nothing was broken in either half: the form posts to
`POST /v1/applications`, the route was registered, and every test of both sides
passed. The site publishes on every merge to main and the control plane only
moves when a `v*` tag is promoted, so the careers page went live the moment its
pull request merged while the API it posts to was still serving v1.1.1, twenty
two commits behind. `POST /v1/applications` answered 404 and the form's `catch`
turned that into the only sentence a visitor ever saw.

`tools/routecheck` asks the deployment rather than the tree, because a check
comparing the site against main's `server.ts` passes on exactly this failure.
It runs before the publish, and it fails when it cannot establish an answer
rather than reporting a pass it did not earn.

This does not by itself restore the careers form. The route reaches production
when a `v*` tag is promoted, as every control plane change does. What changes
today is that the site can no longer be published in front of an API that does
not serve it.

Every control plane URL the site builds is now declared in
`www/lib/control-plane-routes.ts`, and a call site that builds one anywhere else
fails the `www` gate naming the file and the line.
