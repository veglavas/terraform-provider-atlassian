---
page_title: "Jira Service Management request types"
subcategory: ""
description: |-
  Manage JSM request types and understand the remaining portal configuration boundaries.
---

# Jira Service Management request types

Use `atlassian_jsm_request_type` to create a customer request type mapped to an **existing parent issue type** in a company-managed (classic) service project. A request type is not an issue type: several request types may use the same issue type, workflow, and custom fields.

Requires service desk administrator permissions. The public create/delete APIs are documented as **experimental**; the provider sends the required experimental opt-in header. Team-managed (next-gen) projects are not supported by this resource.

## Example

```hcl
# Reuse your managed service project, including one created in this apply.
data "atlassian_jsm_service_desk" "service" {
  project_id = atlassian_jira_project.service.id
}

variable "request_issue_type_ids" {
  description = "Existing parent issue type IDs keyed by a stable logical name."
  type        = map(string)
}

resource "atlassian_jsm_request_type" "lifecycle" {
  for_each = var.request_issue_type_ids

  service_desk_id = data.atlassian_jsm_service_desk.service.id
  issue_type_id   = each.value # Or reference an existing issue type resource's ID.
  name           = each.key

  lifecycle {
    prevent_destroy = true
  }
}
```

For example, map equipment and access request types to your existing service-request issue types. Keep existing workflow/automation references; do not create duplicate issue types. Creating request types does not change when automation runs or automatically assign a request type to work items created directly in Jira.

No manual service desk ID discovery is required. `data.atlassian_jsm_service_desk.service` finds the desk by the managed project's generated ID. Terraform defers the lookup until apply when that ID is unknown, then passes the discovered desk ID to request types. The lookup follows all pages, rejects ambiguous or malformed results, and retries absence for up to 30 seconds while a new desk becomes visible. It can only discover desks the authenticated user can access; a non-service project or inaccessible desk produces an actionable error, not a guessed ID. Retry if provisioning takes longer.

See `examples/jsm-request-types/main.tf` for a from-scratch example: managed service project, issue type, scheme assignment, service desk lookup, and request type. It needs no manually discovered project, service desk, or issue type IDs. The project lead's accountId and provider credentials remain required inputs. Existing projects should reuse their existing resources, not create duplicates.

The request type resource still takes a numeric `service_desk_id` so callers can also reference externally managed desks. It is not a Jira project ID or key.

## Important lifecycle limitations

The documented public API has **no request-type update endpoint**. Changing name, description, help text, issue type, or service desk requires **replacement**. Deleting a request type removes it from all existing customer requests. Keep `prevent_destroy = true` for production objects and inspect plans carefully; do not remove that protection just to rename a request type.

For a deliberate manual metadata change, update the Terraform configuration to match and refresh state before planning. A response that omits or nulls previously nonempty metadata is treated conservatively as incomplete rather than as a destructive replacement signal; verify the remote metadata before recovering state. Alternatively, explicitly use `ignore_changes` for metadata you choose not to own. Do not use undocumented APIs to imitate update support.

Creation preserves configured attributes because POST responses can be partial and waits up to 30 seconds for individual GET visibility. If visibility fails after Jira returns an ID, that ID is retained in state, but Terraform can mark the resource **tainted**. Inspect Jira and `terraform state show ADDRESS`; only if the object and configuration are correct, use `terraform untaint ADDRESS`, then review a fresh plan. If POST returns no usable ID or fails ambiguously, inspect Jira before retrying to avoid duplicates.

## Portal configuration is separate

New request types have **no portal groups**, so they are hidden from portal browsing. The API also defaults the icon, inherits issue-type status mappings, and exposes required issue-type fields. The provider does not configure:

- Portal group assignment/visibility.
- Request-form field layout and additional fields.
- Icons or customer-facing status mappings.
- Request restrictions, customer access, notifications, or sharing policies.

Configure these separately in Jira's request-type settings. `group_ids` and `portal_id` are read-only observations; manual grouping is not undone by Terraform. Adding groups does not establish access controls. For sensitive employee requests, verify customer visibility/sharing and notification settings before broad rollout.

For direct Jira entry, select the matching request type when creating the parent. For portal entry, the chosen request type supplies the issue-type association. Neither path requires duplicate custom fields or workflows.

## Import existing request types

Configure current metadata before importing; do not create duplicates by name.

```bash
terraform import atlassian_jsm_request_type.example SERVICE_DESK_ID/REQUEST_TYPE_ID
```

Run `terraform plan` and require no unexpected replacements. A data source with `service_desk_id` and `id` can inspect an existing request type without taking ownership, including types hidden from portal browsing.

## Testing and API reference

Mock-backed lifecycle tests run with normal `go test ./...`. Gated live acceptance requires the existing demo-host allowlist and confirmation, normal provider credentials, plus:

```bash
TF_ACC=1 \
ATLASSIAN_TEST_PROJECT_ID=YOUR_DEMO_SERVICE_PROJECT_ID \
ATLASSIAN_TEST_REQUEST_ISSUE_TYPE_ID=YOUR_EXISTING_DEMO_PARENT_ISSUE_TYPE_ID \
go test ./internal/jsm -run '^TestIntegrationRequestType$' -v
```

This discovers the supplied demo project's desk, then creates and deletes a disposable request type, not a production request type. It does not configure portal groups or forms.

Contracts checked against the [official JSM OpenAPI specification](https://dac-static.atlassian.com/cloud/jira/service-desk/swagger.v3.json) on 2026-10-26. Relevant paths: POST the service desk's `/requesttype` collection; GET/DELETE its `/requesttype/{requestTypeId}` member.
