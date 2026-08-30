// The SCIM wire format: errors, list responses, resources, and ETags.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// One thing here is worth more than the rest: the error shape. A provisioning
// client is a robot that will retry forever, and how it behaves depends
// entirely on what it gets back. A 500 makes Okta retry the same broken request
// on a schedule until somebody notices; a 400 with a scimType makes it stop and
// show the administrator what is wrong. So every refusal in this package
// carries a scimType and a sentence a person can act on, and nothing throws a
// bare Error into the response.
//
// The other thing is that a delete for an unknown user is a 404 and is FINE.
// Deprovisioning is the operation most likely to be sent twice: the provider
// retries, an administrator removes somebody who was already removed, two
// syncs overlap. The specification says 404, the providers treat it as done,
// and an implementation that treats it as an error accumulates alarms for a
// state that is exactly what everybody wanted.

export const SCHEMAS = {
  user: 'urn:ietf:params:scim:schemas:core:2.0:User',
  enterpriseUser: 'urn:ietf:params:scim:schemas:extension:enterprise:2.0:User',
  group: 'urn:ietf:params:scim:schemas:core:2.0:Group',
  listResponse: 'urn:ietf:params:scim:api:messages:2.0:ListResponse',
  error: 'urn:ietf:params:scim:api:messages:2.0:Error',
  serviceProviderConfig: 'urn:ietf:params:scim:schemas:core:2.0:ServiceProviderConfig',
} as const

export const CONTENT_TYPE = 'application/scim+json; charset=utf-8'

export class ScimError extends Error {
  readonly status: number
  readonly scimType: string | null

  constructor(status: number, message: string, scimType: string | null = null) {
    super(message)
    this.name = 'ScimError'
    this.status = status
    this.scimType = scimType
  }
}

export function errorBody(status: number, detail: string, scimType?: string | null) {
  return {
    schemas: [SCHEMAS.error],
    // A string, not a number. The specification says string and at least one
    // widely used client parses it as one and fails on a number.
    status: String(status),
    ...(scimType ? { scimType } : {}),
    detail,
  }
}

export interface UserRecord {
  id: string
  externalId: string | null
  userName: string
  active: boolean
  givenName: string | null
  familyName: string | null
  displayName: string | null
  version: number
  createdAt: Date
  updatedAt: Date
  role?: string | null
}

export interface GroupRecord {
  id: string
  externalId: string | null
  displayName: string
  role: string | null
  version: number
  createdAt: Date
  updatedAt: Date
}

export interface GroupMemberRecord {
  memberRef: string
  resourceId: string | null
  userName: string | null
}

/**
 * The ETag for a resource.
 *
 * A weak validator, because it is derived from a version counter rather than
 * from the bytes: two responses with the same version are semantically the same
 * resource even if a field this serialiser formats changes. Claiming a strong
 * validator would be a lie a caching proxy is entitled to act on.
 */
export function etag(version: number): string {
  return `W/"${version}"`
}

export function userResource(user: UserRecord, baseUrl: string): Record<string, unknown> {
  const name =
    user.givenName || user.familyName
      ? {
          ...(user.givenName ? { givenName: user.givenName } : {}),
          ...(user.familyName ? { familyName: user.familyName } : {}),
          formatted:
            [user.givenName, user.familyName].filter(Boolean).join(' ') || user.displayName || undefined,
        }
      : undefined

  return {
    schemas: [SCHEMAS.user],
    id: user.id,
    ...(user.externalId ? { externalId: user.externalId } : {}),
    userName: user.userName,
    active: user.active,
    ...(name ? { name } : {}),
    ...(user.displayName ? { displayName: user.displayName } : {}),
    // The address is a work email because that is what a directory provisions,
    // and it is marked primary because a client that finds no primary picks
    // one arbitrarily.
    emails: [{ value: user.userName, type: 'work', primary: true }],
    meta: {
      resourceType: 'User',
      created: user.createdAt.toISOString(),
      lastModified: user.updatedAt.toISOString(),
      version: etag(user.version),
      location: `${trim(baseUrl)}/scim/v2/Users/${user.id}`,
    },
  }
}

export function groupResource(
  group: GroupRecord,
  members: readonly GroupMemberRecord[],
  baseUrl: string,
): Record<string, unknown> {
  return {
    schemas: [SCHEMAS.group],
    id: group.id,
    ...(group.externalId ? { externalId: group.externalId } : {}),
    displayName: group.displayName,
    members: members.map((m) => ({
      // The resource id when this member has been matched to a user here, and
      // the provider's own reference when it has not. A member the provider
      // sent before the user existed is still reported rather than dropped:
      // pretending a group is smaller than the provider believes is how a
      // reconciliation job decides to add everybody again.
      value: m.resourceId ?? m.memberRef,
      ...(m.userName ? { display: m.userName } : {}),
      ...(m.resourceId ? { $ref: `${trim(baseUrl)}/scim/v2/Users/${m.resourceId}` } : {}),
    })),
    meta: {
      resourceType: 'Group',
      created: group.createdAt.toISOString(),
      lastModified: group.updatedAt.toISOString(),
      version: etag(group.version),
      location: `${trim(baseUrl)}/scim/v2/Groups/${group.id}`,
    },
  }
}

export function listResponse(
  resources: readonly Record<string, unknown>[],
  total: number,
  startIndex: number,
): Record<string, unknown> {
  return {
    schemas: [SCHEMAS.listResponse],
    totalResults: total,
    // One-based, and stated even when empty. A client that reads startIndex to
    // decide whether to ask for another page needs it on every response.
    startIndex,
    itemsPerPage: resources.length,
    Resources: resources,
  }
}

export interface Page {
  startIndex: number
  count: number
}

/** How much of a list to return, from the query parameters. */
export function pageFrom(query: URLSearchParams, maxCount = 200): Page {
  const startIndex = positive(query.get('startIndex'), 1)
  const requested = positive(query.get('count'), 100)
  // Capped rather than refused. A client asking for ten thousand at once is
  // not misbehaving, it is optimistic, and answering with the first two hundred
  // plus an honest totalResults is what lets it page.
  return { startIndex, count: Math.min(requested, maxCount) }
}

function positive(value: string | null, fallback: number): number {
  if (value === null || value.trim() === '') return fallback
  const parsed = Number(value)
  if (!Number.isFinite(parsed) || parsed < 1) return fallback
  return Math.floor(parsed)
}

/**
 * What this implementation supports, as the specification requires it to be
 * published.
 *
 * Written from what the code actually does. A ServiceProviderConfig that claims
 * a capability the server lacks makes a client use it and fail, which is worse
 * than not claiming it: the client would otherwise have used the path that
 * works.
 */
export function serviceProviderConfig(baseUrl: string): Record<string, unknown> {
  return {
    schemas: [SCHEMAS.serviceProviderConfig],
    documentationUri: 'https://antifailure.dev/docs/enterprise/scim',
    patch: { supported: true },
    // Not supported and said so. A bulk request this server answered by
    // ignoring it would be the silent failure this whole package avoids.
    bulk: { supported: false, maxOperations: 0, maxPayloadSize: 0 },
    filter: { supported: true, maxResults: 200 },
    changePassword: { supported: false },
    sort: { supported: false },
    etag: { supported: true },
    authenticationSchemes: [
      {
        type: 'oauthbearertoken',
        name: 'OAuth Bearer Token',
        description: 'A bearer token issued per organization in the Antifailure control plane.',
        primary: true,
      },
    ],
    meta: {
      resourceType: 'ServiceProviderConfig',
      location: `${trim(baseUrl)}/scim/v2/ServiceProviderConfig`,
    },
  }
}

export function resourceTypes(baseUrl: string): Record<string, unknown> {
  const base = trim(baseUrl)
  const types = [
    {
      schemas: ['urn:ietf:params:scim:schemas:core:2.0:ResourceType'],
      id: 'User',
      name: 'User',
      endpoint: '/Users',
      schema: SCHEMAS.user,
      meta: { resourceType: 'ResourceType', location: `${base}/scim/v2/ResourceTypes/User` },
    },
    {
      schemas: ['urn:ietf:params:scim:schemas:core:2.0:ResourceType'],
      id: 'Group',
      name: 'Group',
      endpoint: '/Groups',
      schema: SCHEMAS.group,
      meta: { resourceType: 'ResourceType', location: `${base}/scim/v2/ResourceTypes/Group` },
    },
  ]
  return listResponse(types, types.length, 1)
}

function trim(url: string): string {
  return url.endsWith('/') ? url.slice(0, -1) : url
}
