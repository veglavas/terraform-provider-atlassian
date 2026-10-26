# Atlassian Forms project templates

## Scope and contract

`atlassian_jsm_form_template` is both a resource and a matching data source.
It manages project templates, not issue answers, native request-type fields,
screens, portal groups, or automation rules. No live tenant was used to develop
this feature.

Primary contract: [Forms OpenAPI](https://developer.atlassian.com/cloud/forms/swagger.v3.json),
retrieved from the [versioned OpenAPI link in the REST reference](https://dac-static.atlassian.com/cloud/forms/swagger.v3.json?_v=0.1.45),
and [Forms on Project](https://developer.atlassian.com/cloud/forms/rest/api-group-forms-on-project/).
Reviewed 2026-10-26. The reference links this schema directly. The older
[Form Structure overview](https://developer.atlassian.com/cloud/forms/schemas/form-structure/)
is dated April 2023 and still describes project template writes as unsupported;
the current OpenAPI explicitly defines the following operations and takes
precedence for this implementation.

| Operation | Path | Response |
| --- | --- | --- |
| Create | `POST /project/{projectIdOrKey}/form` | `FormIndexEntry` (partial) |
| Read | `GET /project/{projectIdOrKey}/form/{formId}` | `FormTemplate` |
| Update | `PUT /project/{projectIdOrKey}/form/{formId}` | `FormTemplateIndexEntry` (partial) |
| Delete | `DELETE /project/{projectIdOrKey}/form/{formId}` | success; 404 tolerated |

Writes use `FormTemplateRequest`: required `design`, optional `publish`.
Design contains `conditions`, `layout`, `questions`, `sections`, `settings`.
`FormQuestionRequest.jiraField` is the existing linked Jira field ID.
`publish.portal.portalRequestTypeIds` is an array of integer request type IDs;
when `portal` is present, `submitOnCreate` and `validateOnCreate` are also required.
These operations are not marked experimental and do not need an opt-in header.
GET's optional `requestLanguage` is deliberately omitted to avoid reading a
translated variant into the managed design. No additional query parameters are
sent. The data source looks up by project and UUID, not an ambiguous name match.

The reference lists Browse projects for reads and Administer Jira for template
writes. Use the permission required by the API; the provider does not assume an
email/token automatically grants administrative access.

## Authentication, routing and retries

[Authentication and usage](https://developer.atlassian.com/cloud/forms/rest/intro/)
explicitly support Basic auth at
`https://api.atlassian.com/jira/forms/cloud/{cloudId}`. The other documented server,
`https://api.atlassian.com/ex/jira/{cloudId}/forms`, is the OAuth route. This provider
uses its existing email/API-token Basic credentials and the first route.

Cloud ID is discovered with `GET {tenant-origin}/_edge/tenant_info`, documented in
both the Forms usage guide and
[Atlassian Support's cloud ID instructions](https://support.atlassian.com/jira/kb/retrieve-my-atlassian-sites-cloud-id/).
No `cloud_id` input is necessary. Use the tenant origin as provider `url`, without
an API path, query, fragment or URL-embedded credentials. Only successful UUID
results are cached per client; failures/cancellations remain retryable, and the
cache is race-safe. Parallel first calls can perform duplicate read-only discovery.

Forms clients reuse `internal/atlassian` authentication, User-Agent, buffered
bodies and retry handling. The gateway host is fixed in code. Relative paths are
validated and path identifiers escaped. Redirects are disabled for both discovery
and Forms calls. An internal Go transport injection supports local mocks; there
is no user-configurable credential-forwarding URL. Forms error responses and
transport error strings are not echoed into diagnostics because proxies may echo
credentials or submitted data.

Template POST is **never automatically retried**, including 429 and 5xx, because
there is no documented idempotency key. A timeout, unreadable/missing UUID response
or server error can mean creation succeeded. Inspect the documented project form
index using an authorized read and import `project_id/form_uuid` before applying
again. There is no guessed deduplication by name. PUT, GET and DELETE retain the
shared bounded retry/backoff behavior. This feature does not change POST retry
behavior for other resources.

After a successful POST the provider saves the UUID and plan values, then waits
up to 30 seconds for GET visibility. A failed visibility check retains state but
Terraform may taint the resource. Inspect the remote template and state; untaint
only after verifying it is correct. Do not blindly reapply a tainted create.
Ordinary refresh 404 removes state; DELETE 404 succeeds. Atlassian may also return
404 for an inaccessible project/template, so lost permissions can appear as
absence under this repository's required missing-read convention.

## JSON interface and drift

The resource inputs are `project_id`, `design`, and optional/computed `publish`;
`id` is the template UUID. Changing the project requires replacement. Update
replaces the complete design. Omit `publish` to retain remote settings (updates resend the last observed publishing
object, because omitted-PUT merge/reset semantics are undocumented); explicitly
supply empty request-type ID arrays to unpublish. An empty object does not promise
unpublishing because the API does not document reset semantics for omitted parts.

The local framework custom JSON type implements semantic equality for object key
order, whitespace and equivalent numbers, while preserving array order and JSON
value types. This avoids a new external dependency and does not use plain string
comparison for drift. Question IDs form an API map; question rendering order
belongs to the layout array. There is no Terraform set of ordered questions.

The embedded `internal/jsm/form_template_schema.json` is the reachable subset of
the official `FormTemplateRequest` and `FormDesign` schemas, with descriptive
metadata omitted. Validation checks JSON syntax, object/array/scalar types,
required properties and documented enums. Unknown properties are preserved for
forward compatibility. Layout objects remain opaque, as in the official schema;
we cannot validate rendering, layout/question references or tenant field
compatibility locally. Terraform unknown values skip validation until known.

The last applied configuration shape is stored in private resource state. Refresh
ignores optional schema properties omitted by that configuration (for example
server-added `language`), while checking configured values, required properties,
collection keys and ordered arrays. API-managed optional metadata is not rejected
solely because it is nullable in a response. The data source and imports expose the
complete response design; imports have no previous configuration shape. Align
configuration with that observed design before subsequent changes. GET returns
`defaultAnswer.users` objects, while writes require account ID strings; both
resource and data source normalize those defaults to the write representation.
Partial write index responses never erase the submitted design or publication.

## Layout contract gap and examples

The [complete official create payload](../../internal/jsm/testdata/form-template-official.json)
is reproduced from the reference example. Its `layout` is `[{}]`, and its questions,
conditions and sections are empty. It is an honest example of the documented
request envelope, **not a demonstrated usable form with rendered questions**.
The OpenAPI only describes layout as an array of ADF documents with section
metadata. Neither it nor the linked Form Structure page specifies the Forms
extension nodes needed to render linked questions. Generic ADF documentation does
not supply that Forms-specific contract. We therefore do not generate or guess
ADF widgets.

The [resource example](../../examples/resources/atlassian_jsm_form_template/resource.tf)
retrieves a valid source design with the data source and maps a source question
to `customfield_...` using `jiraField`. The [three-form JML example](../../examples/jml-forms/)
manages joiner/mover/leaver templates, preserves valid layouts and maps existing
questions to existing custom fields and request types. Designs can also be
provided as normalized JSON from a previously retrieved template. Neither path
requires UI clicking when valid source designs already exist. Bootstrapping
useful forms from nothing remains blocked by the missing public Forms layout
contract; we do not represent the official placeholder as a working JML design.

## Field linking, Summary and cascading selects

[Link a form field to a Jira field](https://support.atlassian.com/jira-service-management-cloud/docs/link-a-form-field-to-a-jira-field/)
documents linking to most Jira fields, with matching field types, one link per
Jira field within a form, and no required fields in conditional sections (including
linked Summary). It does not establish universal custom-field compatibility.
Tenant contexts, screens, permissions and field types still matter. No choices
are inferred or synchronized from `jiraField` by this provider.

[Form fields versus Jira fields](https://support.atlassian.com/jira-service-management-cloud/docs/what-are-form-fields-and-jira-fields/)
distinguishes native request fields from Forms. Atlassian's UI has duplicate-field
reconciliation, but the project template CRUD contract does not document a native
request-field removal operation or guarantee identical side effects. This resource
does not remove native Summary, satisfy its requirement automatically, or promise
a custom-fields-only portal. [Linked-field defaults troubleshooting](https://confluence.atlassian.com/servicemanagement/jira-service-management-forms-not-honoring-linked-field-value-1142246104.html)
also warns that hidden request-field preset values can override linked form input.

Cascading select evidence must be interpreted carefully. The schema's `cc` question
type alone proves neither Jira cascading-field mapping nor choice synchronization.
The legacy [ProForma linked-field troubleshooting](https://confluence.atlassian.com/proforma/troubleshooting-linked-fields-1087521368.html)
(last modified June 2024) excludes custom cascading choices; it is not sufficient
evidence to declare current Cloud support impossible. Current Cloud support's
[setting cascading values via API](https://support.atlassian.com/jira/kb/set-jsm-forms-cascading-select-field-values-via-api-in-jira-cloud/)
documents request submission through `/rest/servicedeskapi/request`, a mapped
question, parent/child option IDs encoded as a choice, and availability on the
request type's create screen. That is narrower than a documented template-creation
mapping and synchronization contract. We pass documented `cc`/`jiraField` JSON
through, but make no universal compatibility or synchronized-choice claim. No
special cascading mapper, submission endpoint, or screen remapping is implemented.

## Verification

Tests cover the official fixture, JSON schema and semantic normalization,
publication arrays, unknown references, full mock-backed Terraform CRUD/data
source/import, API defaults and real drift, partial responses, missing reads,
idempotent deletes, error preservation, routing/authentication, redirect rejection,
no POST replay, safe discovery caching and shared retries. The lifecycle test uses
`resource.UnitTest` with injected local clients and no real credentials. Run:

```sh
TF_ACC= go test ./...
TF_ACC= go test -race ./internal/atlassian ./internal/jsm ./internal/provider
TF_ACC= go test ./internal/jsm -run '^TestFormTemplateLifecycle$' -count=1
go vet ./...
go build ./...
tfplugindocs generate
```

These tests require local listening sockets (HTTP mocks and Terraform plugin RPC),
not access to a Jira tenant. Do not run `make testacc` or real-Jira integration tests
for this feature without a separate authorization.

### Local verification results (2026-10-26)

- `go vet ./...` and `go build ./...`: passed, with a writable temporary Go cache.
- Native in-process `TestFormTemplateProtocolLifecycle`, JSON/schema/official
  example tests, and provider schema/registration tests: passed. The protocol
  test exercises real framework planning/apply/read/import/data-source/delete
  calls and private-state normalization with an in-memory HTTP mock.
- Forms HTTP unit tests also passed under the race detector using a temporary Go
  test overlay that replaces only the local-server helper with an in-memory
  `httptest.ResponseRecorder` transport. The same handlers and assertions ran;
  this is supplementary validation, not a claim that socket-based tests passed.
- `TF_ACC= go test ./...` and the Terraform CLI lifecycle test cannot complete in
  this sandbox: local listening sockets fail with `operation not permitted`.
  Run the checked-in tests in CI with loopback sockets before merging.
- Documentation was generated with the repository's `tfplugindocs` v0.24.0
  templates using `generate --provider-name atlassian --providers-schema ...`.
  The JSON was exported directly from the real framework provider schema to avoid
  starting a Terraform plugin socket. Only the new Forms reference pages were
  copied into the repository. Terraform formatting and `git diff --check` pass.

No real credentials, tenant mutations, acceptance runs, commits, pushes or releases
were used. The CRUD implementation is ready for review and normal CI, with the
layout bootstrap limitation above still unresolved by the public contract.
