# Custom field contexts and cascading options

Status: implemented locally; not yet released or live-acceptance verified. See [usage and migration guidance](../guides/custom-field-options.md).

Implementation names: `atlassian_jira_custom_field_context` and `atlassian_jira_custom_field_option`, each with a corresponding data source. Field/context/parent changes replace options; project-scoped context scope changes replace contexts. Global context destroy and field/scope replacement plans are rejected because Jira prohibits deleting global contexts. Names, descriptions, option values, and disabled state update in place. Ordering and default values remain out of scope.

## User need

Manage a Jira cascading-select field such as Department/Unit entirely through Terraform. Configuration supplies a department-to-units map; departments are parent options and their units are child options. The existing custom field must retain its ID.

Example input:

```hcl
variable "department_units" {
  description = "Department names mapped to their unit names."
  type        = map(list(string))
}
```

The field resource already supports the `cascadingselect` type. The new, separate resources manage contexts and options without recreating the field. Adding a variable alone still does not populate Jira.

## Proposed scope

Two resource families, each with a corresponding data source:

1. **Custom field context:** identify and manage a field context, including its intended project and issue-type scope.
2. **Custom field option:** manage an option within a context, with an optional parent option ID for cascading selects, its value, and disabled state.

Keep options separate from the existing custom field resource so changing options does not recreate the field. Resource names and schemas are documented in the generated resource reference.

Terraform should create parent options first and pass their IDs into child options through resource references. Separate resources let callers manage individual options without claiming ownership of every option in a context. Do not treat omission as permission to delete unrelated options.

## API contract verification

The current Jira REST API v3 contracts were verified against Atlassian's OpenAPI specification on 2026-10-23 (source linked in the usage guide). Future changes should re-verify contracts for context creation, scope updates, option creation/update/deletion, and pagination. Relevant API families:

- `/rest/api/3/field/{fieldId}/context`
- `/rest/api/3/field/{fieldId}/context/{contextId}/option`

Confirm authentication requirements, response shapes, parent-child constraints, deletion behavior for options already used by issues, and supported ordering operations. Do not assume the field-options API and app-owned field-options API are interchangeable.

New fields may already have a default context. Provide a supported way to discover/import it instead of blindly creating an overlapping context. Names alone must not silently select an ambiguous context or option.

## Lifecycle and safety

- Implement Create, Read, Update, Delete, and ImportState for both resource types.
- Use stable API IDs and documented composite import IDs.
- Read all managed attributes for drift detection; handle missing objects by removing state.
- Delete must tolerate 404 and propagate other errors.
- Preserve field IDs when adding or changing contexts/options.
- Distinguish disabling an option from deleting it; deletion can affect existing issue values.
- Establish whether an option can change parents in place; require replacement if unsupported.
- Keep parent-before-child creation and child-before-parent deletion dependencies explicit.
- Handle shared-context updates safely: Jira batches option writes, so investigate serialization and concurrent sibling operations.
- Preserve ordered collections where exposed; do not add ordering support until its API and ownership semantics are understood.
- Define behavior for duplicate values, empty departments, scope changes, and eventual consistency.
- Reject destroy and field/scope replacement plans for global contexts before dependent options are deleted; Jira prohibits global context deletion from April 2026. Retain such contexts rather than pretending replacement is a supported migration.
- Incomplete paginated collection responses must produce diagnostics preserving state, not be treated as authoritative absence.

## Tests and acceptance criteria

- Mock HTTP tests validate methods, query parameters, and request bodies.
- Cover pagination, imports, drift, missing parents/contexts, 404 handling, permission errors, and partial responses.
- Cover parent/child creation, value changes, disabling, deletion, and concurrent sibling operations.
- Acceptance tests use randomized names and CheckDestroy.
- A caller can populate Department/Unit from `map(list(string))` without hardcoded option IDs.
- A second apply is empty; changing one unit does not recreate the field or unrelated options.
- Importing an existing default context and options produces a stable plan.

## Relative effort

Moderate, bounded feature work rather than a small extension to field creation. Basic option CRUD is straightforward; context scoping, adoption of existing defaults, dependency-safe deletion, batching/concurrency, and reliable read/import behavior are the substantive work. Contexts and options should be delivered as one resource-family feature with tests, documentation, and examples.
