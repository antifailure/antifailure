// The public surface of the custom roles package.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

export {
  SCOPE_KINDS,
  validate,
  resolverFor,
  effectivePermissions,
  ModelError,
  type Scope,
  type ScopeKind,
  type CustomRole,
  type Grant,
  type RepositoryGroup,
  type Model,
} from './roles.ts'
export {
  CHANGE_KINDS,
  decide,
  policyFor,
  type ChangeKind,
  type ApprovalPolicy,
  type Approver,
  type Approval,
  type Proposal,
  type Decision,
} from './approvals.ts'

export {
  toYAML,
  fromYAML,
  dryRun,
  render,
  PolicyFileError,
  type PolicyFile,
  type Change,
  type DryRun,
} from './policyfile.ts'
