---
page_title: "Upgrading workflow management from v0.1.2"
subcategory: ""
description: |-
  Adopt in-place workflow updates and explicit issue migrations safely.
---

# Upgrading workflow management from v0.1.2

Back up Terraform state and test against a disposable company-managed project
before upgrading. Existing resource addresses, workflow UUIDs, scheme IDs, and
import identifiers remain unchanged. No state removal or re-import is required.

## Workflow updates

Description, status membership, and configured transitions update the existing
workflow through the versioned APIs. The provider fetches current definitions,
validates changes, and preserves unmanaged transitions, status properties, layout,
and transition rules. Validation errors prevent mutation. Workflow **name changes
still require replacement** because the documented update API has no rename field.
Use `lifecycle { prevent_destroy = true }` on production workflows.

`statuses` contains existing Jira status IDs, not invented UUIDs. Terraform keeps
its configured list order even if Jira returns the same statuses in another order.
Creation adds transition `1` (Create) to the first status. To customize it, manage
transition `1` explicitly.

The optional `transitions` map is keyed by transition ID. Supported fields are
`name`, `type` (`INITIAL`, `GLOBAL`, `DIRECTED`), `to_status_id`, and optional
`from_status_ids` (required for DIRECTED). It manages only listed transitions.
Removing a previously managed entry deletes it. Omitting the entire map preserves
all transitions, including previously managed ones. Import does not automatically
claim ownership of transitions. Rules and properties remain unmanaged and are
preserved on updates; rule editing is not exposed in this schema.

When removing any status, configure workflow `status_mappings` as an
old-to-new status ID map. This is required conservatively even when no issues use
that status. These are default mappings across all affected projects
and issue types. Jira validation rejects invalid transitions and migrations. No
automatic destination is selected. Per-project workflow migration overrides are
not currently exposed.

## Active workflow schemes

`default_workflow` and `issue_type_mappings` continue accepting workflow **names**.
The provider resolves IDs, fetches the current scheme version, queries required
mappings, and submits an in-place update. Project assignments and scheme IDs stay
unchanged. Configure explicit migrations when requested:

```hcl
status_mappings = {
  "ISSUE_TYPE_ID" = {
    "OLD_STATUS_ID" = "NEW_STATUS_ID"
  }
}
```

## Projects containing issues

Changing `workflow_scheme_id` now switches the scheme in place and waits for Jira's
migration task. The association uses the same nested `status_mappings` format.
External reassignment refreshes the recorded scheme ID as drift instead of removing
the resource from state. Changing `project_id` still requires replacement.

**Destroy behavior has changed intentionally:** there is no universal default
scheme ID. Without `delete_workflow_scheme_id`, destroy stops managing the association
and leaves Jira unchanged, with a warning. To reassign on destroy, explicitly set
`delete_workflow_scheme_id` and, if needed, `delete_status_mappings` for that reverse
migration. Configure these before destroying a scheme that is still assigned to a
project; Jira will reject deletion of active schemes. Reassignment failures are
errors and retain Terraform state. A deleted project is tolerated.

Migration mappings are operation inputs, not persistent Jira properties; refresh
retains their configured values. Remove obsolete mappings before a different
migration. Existing definitions and associations are refreshed independently.

## Failures and recovery

HTTP 409 is an explicit error, not an invitation to destroy or recreate resources.
Wait for competing Jira changes and refresh before retrying. Async operations are
polled for up to 30 minutes and respect cancellation. Failed tasks, unknown task
statuses, and unavailable task responses are errors. A timeout does not cancel
Jira's task: inspect Jira and refresh state before retrying. Do not run competing
workflow changes concurrently against the same scheme; consider `-parallelism=1`
for related workflow and scheme migrations.

See `examples/jml-workflow` for adoption and in-place updates. Mock tests cover
validation, preservation, conflicts, mapping requirements, task failures, and
association drift. The opt-in `TestIntegrationWorkflowManagement_populatedProject`
creates a disposable project and issue, updates an active workflow and scheme,
and switches the populated project. It requires `TF_ACC=1`, Jira credentials, and
`ATLASSIAN_TEST_FALLBACK_SCHEME_ID`. The fallback must support status `1` (Open),
which that test uses. Live migration verification is required before production use;
mock tests are not evidence that tenant-specific rules or permissions will allow a
migration.
