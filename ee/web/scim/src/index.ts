// SCIM 2.0 provisioning: Users and Groups, managed by the identity provider.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

import { registerExtension } from '@antifailure/api'
import { scimExtension, type ScimOptions } from './routes.ts'

export function install(options: ScimOptions): void {
  registerExtension(scimExtension(options))
}

export { scimExtension, type ScimOptions } from './routes.ts'
export { parseFilter, attributesIn, FilterRefused, type Filter, type CompareOperator } from './filter.ts'
export { normalisePatch, splitPath, asBoolean, asString, PatchRefused, type Change } from './patch.ts'
export {
  SCHEMAS,
  CONTENT_TYPE,
  ScimError,
  errorBody,
  etag,
  groupResource,
  listResponse,
  pageFrom,
  resourceTypes,
  serviceProviderConfig,
  userResource,
  type UserRecord,
  type GroupRecord,
  type GroupMemberRecord,
} from './scim.ts'
export {
  authenticate,
  createGroup,
  createUser,
  deleteGroup,
  deleteUser,
  getGroup,
  getUser,
  listGroups,
  listUsers,
  replaceGroup,
  updateUser,
  type Caller,
  type UserInput,
  type GroupInput,
} from './store.ts'
