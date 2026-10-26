# Jira Service Management request types

## Goal

Manage customer request types declaratively so the same Joiner/Mover/Leaver issue types and workflows can support direct Jira and portal entry. Keep the provider generic: no hardcoded project, organization, or workflow IDs. Do not change post-validation automation triggers.

## Resource and data source

`atlassian_jsm_request_type`:

- Resource: create, read, delete, composite import `SERVICE_DESK_ID/REQUEST_TYPE_ID`.
- Data source: read by numeric service desk ID and request type ID, including hidden types.
- Configurable: `service_desk_id`, `issue_type_id`, `name`, `description`, `help_text`.
- Computed: `id`, `portal_id`, `group_ids` (unordered, observation only).
- All configurable changes require replacement; the documented API has no update endpoint. Implement Update defensively with a diagnostic, never fake an in-place update.
- Require service desk numeric IDs, not project IDs/keys, to avoid API alias/canonical-ID drift.
- Read all attributes and remove missing resources on 404. Preserve state on malformed or mismatched successful reads. Delete tolerates 404.
- No automatic adoption by name, no bulk ownership, no changes to existing request types unless imported.

## Service desk discovery

`data.atlassian_jsm_service_desk` takes a numeric Jira `project_id` (including a managed project's generated ID) and returns the associated service desk `id`, `project_key`, and `project_name`. Terraform defers unknown project IDs until apply. No manual desk ID discovery is required. This is a lookup, not a new service desk provisioning resource.

Use GET `/rest/servicedeskapi/servicedesk?start=...&limit=100`, scan every page by exact project ID, and respect server-capped pages. Reject incomplete pages, repeated offsets/IDs, and ambiguous project matches. Retry complete-scan absence for up to 30 seconds for eventual provisioning; propagate permission/API errors immediately. Explain that only visible, provisioned service desks can be discovered. Do not select by project display name or confuse project IDs with desk IDs.

Test lookup pagination, malformed/ambiguous/error cases, eventual visibility, missing desks, and the real mock-backed Terraform dependency graph: create project -> deferred lookup -> request type, stable second plan, safe teardown. Include a from-scratch example with generated resource references and extend gated acceptance to discover the desk from the demo project ID.

## Verified API contract

Source: [official JSM OpenAPI specification](https://dac-static.atlassian.com/cloud/jira/service-desk/swagger.v3.json), checked 2026-10-26.

- POST `/rest/servicedeskapi/servicedesk/{serviceDeskId}/requesttype`: name, issueTypeId, description, helpText. Requires service desk administrator permissions. Documented experimental endpoint; send `X-ExperimentalApi: opt-in` on every attempt.
- GET `/rest/servicedeskapi/servicedesk/{serviceDeskId}/requesttype/{requestTypeId}`: direct lookup avoids list pagination and hidden-type filtering.
- DELETE same individual path: documented experimental, company-managed (classic) projects only. **Removes the request type from all customer requests.**
- Creation in team-managed (next-gen) projects is handled through issue types, not this resource.
- No documented request-type metadata PUT/PATCH endpoint. No documented writes for portal grouping, request-type form layout, icons, or customer status mappings in this API.
- Creation defaults to no portal groups (hidden from portal browsing), headset icon, inherited status mappings, and required issue-type fields. Read group IDs but never imply they can be configured here.

## Safety and tests

- Preserve POST plan values because responses may be partial; save the ID before a bounded post-create visibility wait. Document tainted-state recovery rather than promising automatic safe retries.
- Warn about destructive replacements; show prevent_destroy in production examples.
- Mock HTTP tests validate methods, paths, query parameters, request bodies, experimental headers, authentication and user agent.
- Test imports, data source, drift, partial creation, malformed reads, permission errors, eventual visibility, 404 reads/deletes, stable second plan, and replacement plans for every configurable attribute.
- Add gated live acceptance using the existing demo-host allowlist/confirmation, randomized names, CheckDestroy, explicitly supplied demo service project/issue type IDs. No live execution without authorization.

## Out of scope

Portal visibility/group assignment, forms, request restrictions, customer access/notifications, automatic assignment of a request type to issues, Jira automation changes, service desk provisioning. These remain separate configuration concerns; creating the object alone does not complete a customer-facing portal.
