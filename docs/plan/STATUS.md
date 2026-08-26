# Status

The honest answer to "does it do X yet". Every component carries one of three
states and nothing else:

| State | Means |
| --- | --- |
| **proven** | The code exists, its tests pass, and the behavior has been exercised end to end against the real thing. |
| **written** | The code exists and passes its tests against a fake that enforces the real service's validation rules. It has never talked to the real service. |
| **planned** | Specified, not built. |

The distinction between proven and written is load bearing. A provider that has
only ever spoken to a fake is written, no matter how good the fake is.

This file is updated in the same pull request as the code it describes.

## Phase 1. Foundation

| Sub-phase | State | Notes |
| --- | --- | --- |
| 1.1 Repository and governance | proven | Governance files, templates, CODEOWNERS, ADRs 0001 and 0002. |
| 1.2 Toolchain pinning and task runner | planned | |
| 1.3 Schemas and code generation | planned | |
| 1.4 Continuous integration and gates | planned | |
| 1.5 Release pipeline | planned | |
| 1.6 Security baseline | planned | |
| 1.7 Documentation site skeleton | planned | |
| 1.8 Azure foundation (Terraform) | planned | Blocked on Q4: no resource groups provisioned. |
| 1.9 Test infrastructure and fakes | planned | |
| 1.10 Events, logging, and redaction | planned | |
| 1.11 Local state store and journal | planned | |

## Supporting packages

| Package | State | Notes |
| --- | --- | --- |
| `internal/clock` | proven | Injected clock with a fake that tests advance explicitly. |
| `internal/secrets` (Value) | proven | 100 percent statement coverage; every fmt, JSON, YAML, and slog path renders the marker. |

## Phases 2 to 14

Not started. See the build plan for the specification.
