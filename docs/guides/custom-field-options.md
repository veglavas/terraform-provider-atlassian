---
page_title: "Custom field contexts and select options"
subcategory: ""
description: |-
  Manage Jira-native select and cascading-select options, and adopt existing contexts safely.
---

# Custom field contexts and select options

The `atlassian_jira_custom_field_context` and `atlassian_jira_custom_field_option` resource families are generic: no organizational names, project keys, field IDs, or people are built into the provider.

Requires **Administer Jira** and a provider release containing these resource families. They do not manage Connect app-owned options, field default values, option ordering, or email/user lookups.

## Scope and ownership

- A context belongs to one custom field. `project_ids = []` means global; `issue_type_ids = []` means all issue types. These are unordered sets.
- Context name and description update in place. Changing a project-scoped context's field or scope **replaces the context**, potentially deleting its options. Global contexts cannot be deleted through Jira's API; the provider rejects their destroy and field/scope replacement plans before dependent options are deleted. Keep `lifecycle { prevent_destroy = true }` on important contexts. Do not blindly apply a replacement plan after import.
- An option resource owns only its option, not all options in the context. Unmanaged options are preserved.
- Option value and disabled state update in place. Changing its field, context, or parent **replaces the option**. Jira's update contract does not support changing its parent.
- Prefer `disabled = true` to deleting options used by existing issues. Removing a resource from configuration requests deletion, not disabling.
- Referencing the parent resource's ID in `parent_option_id` ensures parent-first creation and child-first deletion. Jira refuses to delete a parent that still has children, including unmanaged children.
- Option writes are serialized per context within the provider process. Use Terraform state locking and a single owner for each context; this lock does not coordinate independent applies or external writers.
- Creates and updates wait up to 30 seconds for visibility. A failed visibility check after creation retains the generated ID in state, but Terraform can mark that resource **tainted** and propose replacement. Do not blindly reapply. Inspect Jira and `terraform state show ADDRESS`; only after confirming the object and its configuration are correct, use `terraform untaint ADDRESS`, then refresh and review the plan. Retaining the ID alone does not prevent another POST.

## Adopt an existing field safely

Inspect the existing contexts (the listing is paginated):

```bash
curl -sS -u "$ATLASSIAN_USER:$ATLASSIAN_TOKEN" "$ATLASSIAN_URL/rest/api/3/field/FIELD_ID/context?startAt=0&maxResults=100" | jq
```

Configure the chosen context with its **current** name, description, project IDs, and issue type IDs before importing:

```bash
terraform import atlassian_jira_custom_field_context.example FIELD_ID/CONTEXT_ID
```

Run `terraform plan` and review every difference. Import does not silently narrow a global context. If you need different scope, plan an explicit migration rather than replacing a populated context automatically.

Jira commonly creates a default context when a field is created. The provider deliberately does not discover and take ownership of that context during resource creation. An additional project-scoped context also does **not** remove a global default context or make the field unavailable in other projects. Review all contexts before claiming a field is project-only. Do not attempt to delete a global default through these resources.

The current Jira documentation prohibits deleting global contexts and restricts adding projects or issue types to them from April 2026. Replacing a global context is therefore not a supported scope migration. Plan any migration separately with Jira's supported administration tools and verify existing field values. To stop managing a global context without deleting it, use a `removed` block with `lifecycle { destroy = false }` (Terraform 1.7+) or carefully use `terraform state rm ADDRESS`. These operations retain only the specified object: independently protect or retain managed options, and review the full plan. Explicit CLI-forced replacements (`-replace` or tainted state) must also be avoided for global contexts; Terraform can choose those actions outside the provider's field/scope plan checks.

## Adopt existing options

Inspect the chosen context's options (follow all pages):

```bash
curl -sS -u "$ATLASSIAN_USER:$ATLASSIAN_TOKEN" "$ATLASSIAN_URL/rest/api/3/field/FIELD_ID/context/CONTEXT_ID/option?startAt=0&maxResults=100" | jq
```

The response's `optionId` identifies a cascading child's **parent**. Terraform calls this `parent_option_id`.

```bash
terraform import atlassian_jira_custom_field_option.parent FIELD_ID/CONTEXT_ID/PARENT_OPTION_ID
terraform import atlassian_jira_custom_field_option.child FIELD_ID/CONTEXT_ID/CHILD_OPTION_ID
```

There is no implicit adoption by display value and no automatic deletion of unconfigured options. Duplicate values are validated by Jira. Do not retry a failed POST blindly if Jira returned an unusable response without an ID; inspect the remote options first.

## Cascading selects from a map

See `examples/cascading-select/main.tf` in the repository for a complete example using `map(list(string))`, with one parent per map key and one child per list entry. Empty lists create a parent without children. Repeated unit names in different departments are supported; the example's variable validation rejects duplicates within a department and blank names.

The example creates a new field. For an already managed field, use its existing resource ID; **do not create a second field**. Inspect/adopt its context before adding options.

Map names form Terraform resource keys. Renaming a key can replace the option. If names change frequently, use stable logical keys and separate display names. Use `moved` blocks for intentional key renames to preserve IDs.

## Data sources

- `data.atlassian_jira_custom_field_context`: specify `field_id` and either exact `name` or `id`. Ambiguous names fail. Returns description, project IDs, and issue type IDs.
- `data.atlassian_jira_custom_field_option`: specify `field_id`, `context_id`, and option `id`. Returns value, disabled state, and parent ID (null for top-level options).

## Testing

Mock-backed Terraform lifecycle tests run with normal `go test`. A live acceptance test creates a disposable cascading-select field and project-scoped context:

```bash
TF_ACC=1 ATLASSIAN_TEST_PROJECT_ID=YOUR_TEST_PROJECT_ID go test ./internal/jira -run '^TestIntegrationCustomFieldContextOptions$' -v
```

The existing integration safety allowlist/confirmation applies. Configure `ATLASSIAN_URL`, `ATLASSIAN_USER`, `ATLASSIAN_TOKEN`, and `ATLASSIAN_SWEEP_CONFIRM` as documented for integration tests. Normal unit tests do not run against Jira.

## Verified API contracts

Checked against the [Jira REST API v3 OpenAPI specification](https://dac-static.atlassian.com/cloud/jira/platform/swagger-v3.v3.json) on 2026-10-23:

- Context collection: `/rest/api/3/field/{fieldId}/context`.
- Metadata update/delete: `/rest/api/3/field/{fieldId}/context/{contextId}`.
- Scope mappings: context collection's `/projectmapping` and `/issuetypemapping`, filtered with `contextId`.
- Option collection: `/rest/api/3/field/{fieldId}/context/{contextId}/option`.
- Single-option delete: option collection plus `/{optionId}`.
- Option creation and update wrap entries in `options`. Creation returns IDs in an `options` array. Updates may omit unchanged options from their response.
- The GET option collection's `optionId` query filters children of a parent; it is **not** a lookup by the option's own ID. Reads paginate and match IDs locally.
