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
import { toJSONSchema } from 'zod'
import { CALLBACK_AUDIENCE } from './github/oidc.ts'
import { OIDC_TOKEN_TTL_MS } from './github/exchange.ts'

export const API_VERSION = '1.0.0'

type JsonSchema = Record<string, unknown>

/**
 * What a route accepts, taken from the validators the router actually runs.
 *
 * Absent means the procedure declared no `.input()` at all, which is a
 * different fact from "accepts an empty object" and produces different
 * documentation. Required means the merged validator refuses `undefined`,
 * which is the only question OpenAPI's outer `required` is asking.
 */
type InputSurface =
  | { present: false }
  | { present: true; schema: JsonSchema; required: boolean }

function procedureInput(path: string): InputSurface {
  const record = appRouter._def.procedures as unknown as Record<
    string,
    { _def: { inputs?: unknown[] } }
  >
  const inputs = record[path]?._def.inputs ?? []
  if (inputs.length === 0) return { present: false }
  const schemas = inputs.map((input) => {
    // Describe what a caller may send, not the post-parse output. Zod defaults
    // are optional on input and present on output; using the output view marks
    // every defaulted field as required and makes generated clients stricter
    // than the route they call.
    const schema = toJSONSchema(
      input as Parameters<typeof toJSONSchema>[0],
      { io: 'input' },
    ) as JsonSchema
    delete schema.$schema
    return schema
  })
  // Asked of the validator rather than inferred from the JSON Schema, because
  // the two disagree. `z.object({...}).default({...})` renders as an object
  // schema carrying a `default`, which reads as required, and it accepts an
  // absent input. `.parse(undefined)` is the same call tRPC makes, so it is
  // the only source that cannot drift from the route.
  const required = inputs.some((input) => {
    const validator = input as { safeParse?: (value: unknown) => { success: boolean } }
    // A validator this cannot ask is documented as required, which is the
    // conservative direction: a caller that sends the input always works, and
    // a caller told the input was optional when it is not gets a 400.
    if (typeof validator.safeParse !== 'function') return true
    return !validator.safeParse(undefined).success
  })
  return { present: true, schema: schemas.length === 1 ? schemas[0]! : { allOf: schemas }, required }
}

function operationId(path: string): string {
  return `trpc_${path.replace(/[^a-zA-Z0-9]+/g, '_')}`
}

const json = (schema: JsonSchema) => ({
  'application/json': { schema },
})

// The three refusal bodies this service actually puts on the wire. They are
// genuinely different shapes and one schema covering all three describes none
// of them, which is what an `error` that was "a string or an object" did: it
// validated against the readiness 503, which carries no `error` at all, and
// against the generic 500, whose `code` is a string where tRPC's is an integer.
//
// Refusal      hand-written routes: /v1/events and /v1/environments/:id.
// ServerFailure the unhandled-error handler, the only body carrying requestId.
// TrpcFailure  anything under /trpc, formatted by tRPC and not by this code.
const refusal = (description: string) => ({
  description,
  content: json({ $ref: '#/components/schemas/Refusal' }),
})

const trpcFailure = (description: string) => ({
  description,
  content: json({ $ref: '#/components/schemas/TrpcFailure' }),
})

const serverFailure = {
  description:
    'The request failed for a reason the control plane did not expect. The body carries a ' +
    'correlation id; nothing about the request payload is logged or returned.',
  content: json({ $ref: '#/components/schemas/ServerFailure' }),
}

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
        operationId: 'getLiveness',
        summary: 'Liveness',
        security: [],
        description: 'Reports whether the control-plane process is answering. It does not touch the database.',
        responses: {
          '200': {
            description: 'The process is answering.',
            content: json({
              type: 'object',
              required: ['ok'],
              properties: { ok: { type: 'boolean', const: true } },
              additionalProperties: false,
            }),
          },
          '500': serverFailure,
        },
      },
    },
    '/readyz': {
      get: {
        operationId: 'getReadiness',
        summary: 'Readiness and deployed build',
        security: [],
        description:
          'Checks the application database and reports the version and commit that are actually serving.',
        responses: {
          '200': {
            description: 'The process and its database are ready.',
            content: json({ $ref: '#/components/schemas/Readiness' }),
          },
          // The same schema as the 200, with ready false and a reason. It is
          // NOT an error envelope: the body carries no `error` member at all,
          // and a client that parsed it as one read undefined and reported the
          // service healthy.
          '503': {
            description:
              'The process is alive and its database did not answer. The body reports the ' +
              'deployed version and commit, which is what a rollback decision needs.',
            content: json({ $ref: '#/components/schemas/Readiness' }),
          },
          // Reachable, and not from this handler: it catches the database error
          // itself and answers 503. A 500 here is the middleware in front of it
          // throwing, which is the same for every route in the service, and the
          // body is unusual enough to be worth naming wherever it can happen.
          '500': serverFailure,
        },
      },
    },
    '/v1/events': {
      post: {
        operationId: 'ingestEngineEvents',
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
                        // Not an enum, because the server is not one. An
                        // unknown type is stored and changes nothing, on
                        // purpose: that is what lets an older control plane
                        // ingest a newer engine's events instead of refusing
                        // them. A closed enum here would make every generated
                        // client reject at the boundary exactly the events the
                        // server was built to accept, and would turn a
                        // forward-compatible design into a breaking one.
                        //
                        // The known set is published as an extension and as
                        // examples, which is discovery without refusal.
                        type: {
                          type: 'string',
                          minLength: 1,
                          description:
                            'One of x-antifailure-event-types, or a newer type this control ' +
                            'plane does not know. An unknown type is stored and projects nothing.',
                          examples: [...EVENT_TYPES],
                        },
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
            content: json({ $ref: '#/components/schemas/IngestResult' }),
          },
          '207': {
            description: 'Some events were rejected. The outcomes array says which and why.',
            content: json({ $ref: '#/components/schemas/IngestResult' }),
          },
          '400': refusal(
            'The body is not JSON, or it carries no events array. Nothing was stored.',
          ),
          '401': refusal('The token is not valid.'),
          '403': refusal(
            'The token is valid and the organization is suspended. Events are refused rather ' +
              'than dropped, so an engine should keep the batch and send it again after ' +
              'retryAfterSeconds.',
          ),
          '413': refusal('The batch is larger than the limit.'),
          '429': {
            description: 'Too many events. Retry-After says how long to wait before sending the same batch again.',
            headers: { 'Retry-After': { schema: { type: 'integer' } } },
            content: json({ $ref: '#/components/schemas/Refusal' }),
          },
          '500': serverFailure,
        },
      },
    },
    '/v1/auth/github-oidc': {
      post: {
        operationId: 'exchangeWorkflowIdentity',
        summary: 'Exchange a GitHub Actions workflow identity for an engine token',
        description:
          'How a job in a customer\'s CI authenticates with no token, no environment variable ' +
          'and no repository secret. The workflow asks GitHub for an identity token with the ' +
          `audience ${CALLBACK_AUDIENCE} (it needs \`id-token: write\`), posts it here, and ` +
          'receives an engine token good for ' +
          `${Math.round(OIDC_TOKEN_TTL_MS / 60000)} minutes on POST /v1/events.\n\n` +
          'The signature, issuer, audience and expiry are all checked, and none of that is what ' +
          'decides which organization the token belongs to. A signed identity proves which ' +
          'repository a job runs in and nothing about who that repository belongs to, because ' +
          'anybody can run Actions in a repository they own. Access comes from a claim on the ' +
          'repository instead.\n\n' +
          'Most callers never make that claim themselves. When a repository has no claim and ' +
          'exactly one organization holds a live GitHub App installation on its owner, the ' +
          'claim is created on this first exchange and recorded as having come from the ' +
          'installation. What is refused is a repository with no claim AND no installation to ' +
          'stand in for one: an owner nobody has installed the App on reaches nobody, and an ' +
          'owner two organizations have installed on is refused rather than guessed at. ' +
          'POST /v1/oidc/bindings claims one by hand, for a repository the App is not ' +
          'installed on.',
        requestBody: {
          required: true,
          content: {
            'application/json': {
              schema: {
                type: 'object',
                required: ['token'],
                properties: {
                  token: {
                    type: 'string',
                    description: `The workflow identity token, minted for audience ${CALLBACK_AUDIENCE}.`,
                  },
                },
              },
            },
          },
        },
        responses: {
          '200': {
            description:
              'An engine token, returned once and stored nowhere. Present it as a bearer token ' +
              'on POST /v1/events exactly as a static one.',
            content: {
              'application/json': {
                schema: {
                  type: 'object',
                  required: ['token', 'expires_at'],
                  properties: {
                    token: { type: 'string' },
                    expires_at: { type: 'string', format: 'date-time' },
                    org_id: { type: 'string', format: 'uuid' },
                    repository: { type: 'string', description: 'owner/name.' },
                  },
                },
              },
            },
          },
          '400': { description: 'The body is not JSON, or carries no token.' },
          '401': {
            description:
              'The identity token did not verify. `reason` is one of malformed, bad_algorithm, ' +
              'no_key, invalid_signature, wrong_issuer, wrong_audience, expired, not_yet_valid, ' +
              'no_repository or keys_unavailable, and `error` is a sentence the workflow author ' +
              'can act on.',
          },
          '403': {
            description:
              'The identity verified and grants nothing. `reason` is no_binding when the ' +
              'repository has not been claimed, binding_revoked when the claim was withdrawn, ' +
              'or installation_suspended.',
          },
          '429': {
            description: 'Too many exchanges for this repository. A job needs one credential.',
            headers: { 'Retry-After': { schema: { type: 'integer' } } },
          },
        },
      },
    },
  }

  // The tRPC procedures. They are described rather than fully typed, because
  // tRPC's own client carries the types and this document exists for callers
  // that are not that client.
  const permissions = declaredPermissions()
  for (const { path, type } of listProcedures()) {
    const permission = permissions.get(path)
    const input = procedureInput(path)
    const isQuery = type === 'query'
    paths[`/trpc/${path}`] = {
      [isQuery ? 'get' : 'post']: {
        operationId: operationId(path),
        summary: path,
        description: permission
          ? `Requires the ${permission} permission, held by: ${rolesWith(permission).join(', ')}.`
          : 'Requires no permission.',
        security: permission ? [{ session: [] }] : [],
        ...(isQuery
          ? {
              // A query with no validator has no `input` parameter to name, and
              // one whose validator refuses `undefined` has a mandatory one.
              // Publishing `required: false` for every query told a generated
              // client that `GET /trpc/environments.get` with no input was a
              // legal call. It is a 400.
              ...(input.present
                ? {
                    parameters: [{
                      name: 'input',
                      in: 'query',
                      required: input.required,
                      description: input.required
                        ? 'JSON-encoded tRPC input. This route refuses a request without it.'
                        : 'JSON-encoded tRPC input. Every field has a default, so it may be omitted.',
                      content: { 'application/json': { schema: input.schema } },
                    }],
                  }
                : {}),
            }
          : {
              parameters: [{
                name: 'x-antifailure-csrf',
                in: 'header',
                required: true,
                description: 'The CSRF token returned by GET /auth/session.',
                schema: { type: 'string', minLength: 32 },
              }],
              // Required for every mutation, including the three that read no
              // input, and that is a statement about the transport rather than
              // about the validator: tRPC answers a POST carrying no
              // content-type with 415 before any validator runs. Measured, not
              // assumed.
              //
              // What the validator decides is the SCHEMA. A route with no
              // `.input()` used to publish `additionalProperties: false`, which
              // says the only legal body is `{}`. The route reads no body at
              // all and accepts any JSON object, so the document was stricter
              // than the thing it describes.
              requestBody: {
                required: true,
                ...(input.present
                  ? {}
                  : {
                      description:
                        'This route reads no input. The transport still requires a JSON content type, so send {}.',
                    }),
                content: json(
                  input.present
                    ? input.schema
                    : { type: 'object', description: 'Ignored. The route declares no input validator.' },
                ),
              },
            }),
        responses: {
          '200': {
            description: 'The tRPC result envelope.',
            content: json({ $ref: '#/components/schemas/TrpcResult' }),
          },
          '400': trpcFailure('The input did not satisfy this route\'s validator.'),
          ...(permission
            ? {
                '401': trpcFailure('No session.'),
                '403': trpcFailure(`The role does not hold ${permission}.`),
              }
            : {}),
          '500': trpcFailure(
            'The route threw. tRPC formats this one, so it is a tRPC envelope rather than the ' +
              'ServerFailure body, and the stack is withheld: it names internal paths and ' +
              'table names to anyone who can provoke an error.',
          ),
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
    servers: [{
      url: 'https://app.antifailure.dev',
      description: 'Antifailure hosted control plane',
    }],
    components: {
      securitySchemes: {
        session: { type: 'apiKey', in: 'cookie', name: 'af_session' },
        engineToken: { type: 'http', scheme: 'bearer' },
      },
      schemas: {
        Refusal: {
          type: 'object',
          description:
            'A refusal from a hand-written route. `error` is one sentence for a person, and it ' +
            'names no internal path, table, or query.',
          properties: {
            error: { type: 'string' },
            retryAfterSeconds: {
              type: 'integer',
              minimum: 0,
              description: 'Present on 403 and 429. Wait this long before sending the same request again.',
            },
          },
          required: ['error'],
          additionalProperties: false,
        },
        ServerFailure: {
          type: 'object',
          description:
            'The unexpected path. Hono answers an unhandled error with plain text by default, ' +
            'and a caller should not need a second parser exactly when the service is least ' +
            'healthy, so this shape is guaranteed instead.',
          properties: {
            error: {
              type: 'object',
              required: ['code', 'message', 'resolution'],
              properties: {
                code: {
                  type: 'string',
                  description: 'A code from the published catalog at https://antifailure.dev/errors.v1.json.',
                },
                message: { type: 'string' },
                resolution: { type: 'string' },
              },
              additionalProperties: false,
            },
            requestId: {
              type: 'string',
              description:
                'The same value as the x-request-id response header. Quote it when reporting ' +
                'the failure: it is the only thing that ties this answer to a log line, ' +
                'because nothing about the request payload is logged.',
            },
          },
          required: ['error', 'requestId'],
          additionalProperties: false,
        },
        TrpcFailure: {
          type: 'object',
          description:
            'The tRPC error envelope, produced by tRPC rather than by this service. `code` is ' +
            'the JSON-RPC integer; the readable one is `data.code`.',
          properties: {
            error: {
              type: 'object',
              required: ['message', 'code', 'data'],
              properties: {
                message: { type: 'string' },
                code: { type: 'integer' },
                data: {
                  type: 'object',
                  properties: {
                    code: { type: 'string', examples: ['UNAUTHORIZED', 'FORBIDDEN', 'BAD_REQUEST'] },
                    httpStatus: { type: 'integer' },
                    path: { type: 'string' },
                  },
                  additionalProperties: true,
                },
              },
              additionalProperties: true,
            },
          },
          required: ['error'],
          additionalProperties: true,
        },
        TrpcResult: {
          type: 'object',
          required: ['result'],
          properties: {
            result: {
              type: 'object',
              required: ['data'],
              properties: { data: {} },
              additionalProperties: true,
            },
          },
          additionalProperties: false,
        },
        Readiness: {
          type: 'object',
          description:
            'Served by both the 200 and the 503. `version` and `commit` are what is actually ' +
            'deployed, which is the pair a rollback decision is made on.',
          required: ['ready', 'version', 'commit'],
          properties: {
            ready: { type: 'boolean' },
            version: { type: 'string' },
            commit: { type: 'string' },
            reason: {
              type: 'string',
              description: 'Present only when ready is false. Why the database did not answer.',
            },
          },
          additionalProperties: false,
        },
        IngestResult: {
          type: 'object',
          required: ['accepted', 'duplicates', 'rejected', 'unprojected', 'outcomes'],
          properties: {
            accepted: { type: 'integer', minimum: 0 },
            duplicates: { type: 'integer', minimum: 0 },
            rejected: { type: 'integer', minimum: 0 },
            unprojected: { type: 'integer', minimum: 0 },
            outcomes: {
              type: 'array',
              items: {
                type: 'object',
                required: ['id', 'status'],
                properties: {
                  id: { type: 'string' },
                  status: { type: 'string', enum: ['accepted', 'duplicate', 'rejected'] },
                  reason: { type: 'string' },
                  note: { type: 'string' },
                },
                additionalProperties: false,
              },
            },
          },
          additionalProperties: false,
        },
      },
    },
    'x-permissions': PERMISSIONS.map((p) => ({
      name: p,
      description: PERMISSION_DESCRIPTIONS[p],
      roles: rolesWith(p),
    })),
    // The event types this version projects. An extension rather than an enum
    // on the property, so a reader can enumerate them and a validator cannot
    // use them to refuse an event the server accepts.
    'x-antifailure-event-types': [...EVENT_TYPES],
    paths,
  }
}
