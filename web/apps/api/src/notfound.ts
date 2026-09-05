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
 *
 * IT ALSO HAS TO BE TRUE, WHICH IT WAS NOT. This sentence used to say the
 * document "lists every endpoint this control plane serves". Production serves
 * that document with 94 paths and none of the four routes the marketing site
 * calls appears in it, because `boundary.ts` classifies all four `excluded`
 * with stated grounds and route-boundary.test.ts fails if the document ever
 * carries one. So somebody who mistyped `/v1/application` read this line,
 * fetched the document, did not find `/v1/applications` there either, and had
 * been told by us to conclude that the endpoint does not exist. It does.
 *
 * The replacement says what the document IS, which is the surface a client can
 * integrate with, and what it deliberately leaves out, which is the transport
 * three first party callers use. "Some endpoints are not listed" would have
 * been a hedge that helps nobody: a reader needs to know whether the thing
 * they were looking for is the kind of thing that would be in there.
 *
 * Nothing pinned the old sentence, which is why it stayed false through the
 * change that made it false. route-boundary.test.ts now holds this body to the
 * register in both directions: the route it names has to be one this process
 * serves, and the disclaimer has to be present exactly while there are routes
 * served here that the document does not carry.
 */
export function apiNotFound(c: Context): Response {
  return c.json(
    {
      error:
        'No endpoint at this path. GET /openapi.json describes the endpoints a ' +
        'client can integrate with. It is not everything this control plane ' +
        'serves: the wire behind the console, the af command line and the engine ' +
        'is deliberately left out, so a route missing from that document may ' +
        'still exist here.',
    },
    404,
  )
}
