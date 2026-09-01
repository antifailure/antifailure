// The OpenAPI document, generated from the router rather than written.
//
// A hand-written document describes what somebody believed the API was on the
// day they wrote it. This one is built from the same procedure tree the server
// serves, so a route that is added, renamed, or removed changes the document
// without anybody remembering to.

import { PERMISSIONS, PERMISSION_DESCRIPTIONS, rolesWith } from './permissions.ts'
import { appRouter } from './routers/index.ts'
import { declaredPermissions } from './trpc.ts'
import { EVENT_TYPES, MAX_BATCH } from './ingest.ts'

export const API_VERSION = '1.0.0'

/** Walks the router tree and returns every procedure with its kind. */
export function listProcedures(): { path: string; type: 'query' | 'mutation' }[] {
  const out: { path: string; type: 'query' | 'mutation' }[] = []
  const record = appRouter._def.procedures as unknown as Record<string, { _def: { type: string } }>
  for (const [path, procedure] of Object.entries(record)) {
    const type = procedure._def.type
    if (type === 'query' || type === 'mutation') out.push({ path, type })
  }
  return out.sort((a, b) => (a.path < b.path ? -1 : 1))
}

export function openApiDocument(): Record<string, unknown> {
  const paths: Record<string, unknown> = {
    '/health': {
      get: {
        summary: 'Liveness',
        responses: { '200': { description: 'The process is answering.' } },
      },
    },
    '/v1/events': {
      post: {
        summary: 'Send events from an engine',
        description:
          'Authenticated by a per-engine bearer token, not a session. Events are idempotent by ' +
          'id and ordered by sequence within an environment, so a retry is safe and a late ' +
          'arrival does not move an environment backwards. A batch carries at most ' +
          `${MAX_BATCH} events.`,
        security: [{ engineToken: [] }],
        requestBody: {
          required: true,
          content: {
            'application/json': {
              schema: {
                type: 'object',
                required: ['events'],
                properties: {
                  events: {
                    type: 'array',
                    maxItems: MAX_BATCH,
                    items: {
                      type: 'object',
                      required: ['id', 'type', 'occurredAt'],
                      properties: {
                        id: { type: 'string', description: 'Unique within the organization. A repeat is dropped.' },
                        type: { type: 'string', enum: [...EVENT_TYPES] },
                        envId: { type: 'string' },
                        sequence: { type: 'integer', minimum: 0 },
                        occurredAt: { type: 'string', format: 'date-time' },
                        payload: {
                          type: 'object',
                          additionalProperties: true,
                          description:
                            'Type specific. An environment.* event should carry repository ' +
                            '(owner/name) and branch on EVERY event and not only the first, ' +
                            'because the environment row is created from whichever event ' +
                            'arrives first and an event that cannot name its repository ' +
                            'cannot create one. started_at is when the environment began ' +
                            'existing, which is not when the event fired: usage and the expiry ' +
                            'are measured from it, so an environment reported ready after its ' +
                            'build is still billed for the build. ttl_seconds is the declared ' +
                            'lifetime, added to that instant to give the expiry.',
                          properties: {
                            repository: { type: 'string', description: 'owner/name.' },
                            branch: { type: 'string' },
                            pull_request: { type: 'integer', minimum: 1 },
                            started_at: { type: 'string', format: 'date-time' },
                            ttl_seconds: { type: 'number', minimum: 1 },
                          },
                        },
                      },
                    },
                  },
                },
              },
            },
          },
        },
        responses: {
          '202': {
            description:
              'Every event was accepted or was a duplicate. An accepted event that changed no ' +
              'environment row carries a note saying why, and is counted in unprojected; it is ' +
              'stored either way.',
          },
          '207': { description: 'Some events were rejected. The outcomes array says which and why.' },
          '401': { description: 'The token is not valid.' },
          '413': { description: 'The batch is larger than the limit.' },
          '429': {
            description: 'Too many events. Retry-After says how long to wait before sending the same batch again.',
            headers: { 'Retry-After': { schema: { type: 'integer' } } },
          },
        },
      },
    },
  }

  // Studio, for an engine. Described here because an engine that is not this
  // repository's `af` has no other way to learn the shape, and because the
  // claim endpoint is the only place a workload version's body crosses a wire.
  const engineOnly = {
    security: [{ engineToken: [] }],
    responses: {
      '200': { description: 'The answer.' },
      '400': { description: 'The body is not JSON, or is missing a field.' },
      '401': { description: 'The token is not valid.' },
      '403': { description: 'The organization is suspended.' },
      '404': { description: 'No such environment in this organization.' },
      '409': { description: 'This token does not hold the lease.' },
    },
  }
  paths['/v1/workloads/claim'] = {
    post: {
      ...engineOnly,
      summary: 'Take the workload run waiting for an environment',
      description:
        'Moves the oldest requested run for the environment to accepted and returns its ' +
        'compiled version, with a lease. Answers 200 and a null run when nothing is waiting, ' +
        'so a poller can tell that apart from a failure. The engine pulls rather than being ' +
        'told because a workflow_dispatch carries only the inputs the workflow declares.',
    },
  }
  paths['/v1/workloads/runs/{runId}/heartbeat'] = {
    post: {
      ...engineOnly,
      summary: 'Say the run is still going',
      description:
        'Extends the lease and the deadline. A run whose deadline passes with nothing said ' +
        'about it ends as abandoned, which is the control plane admitting it never heard ' +
        'rather than a claim that the work failed.',
    },
  }
  paths['/v1/commands/claim'] = {
    post: {
      ...engineOnly,
      summary: 'Take the runtime commands waiting for this organization',
      description:
        'Teardowns and cancellations, with a lease. Reachable while the organization is ' +
        'suspended, deliberately: a suspension stops new work, and stopping what is running ' +
        'is the opposite of new work.',
    },
  }
  paths['/v1/commands/{id}/ack'] = {
    post: {
      ...engineOnly,
      summary: 'Say what happened to a command',
      description:
        'Only the holder of the lease may acknowledge, and an acknowledgement that matches no ' +
        'row answers 409 rather than 200: an acknowledgement nobody applied is the silent ' +
        'nothing the durable command exists to end.',
    },
  }

  // The tRPC procedures. They are described rather than fully typed, because
  // tRPC's own client carries the types and this document exists for callers
  // that are not that client.
  const permissions = declaredPermissions()
  for (const { path, type } of listProcedures()) {
    const permission = permissions.get(path)
    paths[`/trpc/${path}`] = {
      [type === 'query' ? 'get' : 'post']: {
        summary: path,
        description: permission
          ? `Requires the ${permission} permission, held by: ${rolesWith(permission).join(', ')}.`
          : 'Requires no permission.',
        security: permission ? [{ session: [] }] : [],
        responses: {
          '200': { description: 'The result.' },
          ...(permission
            ? {
                '401': { description: 'No session.' },
                '403': { description: `The role does not hold ${permission}.` },
              }
            : {}),
        },
      },
    }
  }

  return {
    openapi: '3.1.0',
    info: {
      title: 'Antifailure control plane',
      version: API_VERSION,
      description:
        'Environments, runs, verdicts, masking attestations, and network decisions across an ' +
        'organization. The control plane never receives database contents, model traffic, or ' +
        'secrets: it receives events, metadata, and artifacts that were opted in.',
      license: { name: 'MIT', identifier: 'MIT' },
    },
    components: {
      securitySchemes: {
        session: { type: 'apiKey', in: 'cookie', name: 'af_session' },
        engineToken: { type: 'http', scheme: 'bearer' },
      },
    },
    'x-permissions': PERMISSIONS.map((p) => ({
      name: p,
      description: PERMISSION_DESCRIPTIONS[p],
      roles: rolesWith(p),
    })),
    paths,
  }
}
