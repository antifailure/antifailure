// What the server says when it has no route for a path.
//
// One sentence, in one place, because there are two handlers that have to say
// it and they are reached differently. The console registers Hono's not-found
// handler when it is mounted and answers a page there; this is what answers
// when the path belongs to the API, and it is also the whole handler on a
// deployment started with the console switched off.
//
// It exists at all because a path with no route used to be answered by the
// rate limit gate, which runs before routing and could not tell a path that
// does not exist from a route nobody declared a limit for. It answered both
// with 500. On the deployed control plane `GET /v1/health` returned 500 with
// "Add it to ENDPOINT_LIMITS with the reason for the number", which is a
// 500 for a client's typo and an internal instruction served to a stranger.

import type { Context } from 'hono'

/**
 * The API's 404.
 *
 * JSON, because everything else the API answers is JSON and an agent should
 * not need a text parser only for the answer it is most likely to get while
 * finding its way around. The default Hono not-found handler writes plain
 * text, which is what a deployment with the console switched off would
 * otherwise serve.
 *
 * The resolution names an endpoint this same process serves rather than a
 * documentation page, so it cannot rot: if `GET /openapi.json` ever stops
 * answering, the answer to this is broken in a way a test can see.
 */
export function apiNotFound(c: Context): Response {
  return c.json(
    {
      error:
        'No endpoint at this path. GET /openapi.json lists every endpoint this ' +
        'control plane serves.',
    },
    404,
  )
}
