---
page_title: "Per-issue-type field requirements"
subcategory: ""
description: |-
  Manage Jira field requirements without changing scheme defaults or native customer request-form layouts.
---

# Per-issue-type field requirements

`atlassian_jira_field_scheme_requirement` manages one field's required flag for
one issue type in an **existing field association scheme**, using the modern,
experimental [Field schemes API](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-field-schemes/).
It does not use the deprecated field-configuration API.

## Prerequisites and scope

- The API user needs the **Administer Jira** global permission.
- The field must already be associated with the scheme and available to the
  selected issue type. This resource does not create/assign schemes, add/remove
  fields, or change their work-type restrictions.
- Arrange the field's context and Jira create screen separately so users can
  supply a value. A requirement is a constraint, not a layout.
- Schemes can be shared by projects. **Every project using the scheme is affected.**
- Do not assign two Terraform resources ownership of the same scheme, field, and
  issue-type tuple. Importing adopts management of the flag; it does not save the
  prior value for restoration later.
- The experimental API's availability and behavior have not been live-verified
  by this implementation's local tests. Validate in a disposable environment
  before production use. No tenant mutations are part of the unit tests.

Use `data.atlassian_jira_project_field_scheme` to discover a project's scheme ID,
name, and description rather than guessing a legacy field-configuration ID.
Discovery is read-only and does not change a project's scheme assignment.

## Managing selected requirements

```hcl
data "atlassian_jira_project_field_scheme" "existing" {
  project_id = var.project_id
}

resource "atlassian_jira_field_scheme_requirement" "selected" {
  for_each = var.requirements

  scheme_id     = data.atlassian_jira_project_field_scheme.existing.id
  field_id      = each.value.field_id
  issue_type_id = each.value.issue_type_id
  is_required   = each.value.is_required
}
```

`var.requirements` is a map of objects with `field_id`, `issue_type_id`, and
`is_required`. Use readable keys, such as `"Support / Department"`, rather than
encoded tuples. Reference IDs of your existing managed custom fields and issue
types where possible. See [the complete example](../../examples/field-requirements/).

Only the selected issue-type-specific `isRequired` parameter is written. Scheme
defaults, descriptions, renderers, field associations, and other issue types
(including subtasks unless explicitly targeted) are untouched.

The matching requirement data source reads the effective flag. If the API returns
no required override for that issue type, reads fall back to the scheme default.
Removing an explicit override out of band is visible as drift if the effective
value changes. This is not an inventory of explicit overrides. If field search
omits the requirement parameters, the provider reads the parameter endpoint
instead of guessing that the field is optional.

## Update, import, and removal

Changing `is_required` updates in place. Changing scheme, field, or issue type
requires replacement.

```shell
terraform import atlassian_jira_field_scheme_requirement.example \
  '10000:customfield_10000:10001'
```

Replace these illustrative IDs with real ones. Import validates canonical positive
numeric scheme and issue-type IDs and the Jira field ID.

Destroy removes **only the issue type's `isRequired` override**. The field then
inherits the scheme default, which may itself be required. Destroy neither forces
optional nor restores an earlier explicit value. Other parameter overrides remain.
Already-deleted parent schemes/field associations are tolerated. Restricting the
field away from the selected issue type also makes this requirement unavailable
and removes it from state. Errors and malformed/partial collection responses
preserve state rather than implying deletion. Field-list 404s are checked against
the direct scheme endpoint: disabling the experimental feature must not be
mistaken for deleting a scheme.

Writes inspect individual batch results, including HTTP 207 partial failures.
After a successful update, a bounded read-back wait handles eventual consistency.
If verification fails after a successful create, identity is retained for recovery;
refresh and inspect the plan before retrying. Setting the same flag again is
idempotent, but Terraform's normal tainted-resource replacement may first remove
its override, temporarily restoring the scheme default.

## Native customer portal boundary

Native JSM request types have separate field selection, ordering, customer labels,
and presets. This resource does not manage those settings and does not configure
Atlassian Forms templates.

The public JSM API documents a read operation for native request-type fields but
no operation for writing those settings. New request types initially map required
issue-type fields; that creation-time default is not evidence that existing
request forms are synchronized after requirement changes. Unsupported portal
fields may need presets or a different supported value source.

See [native request-field API findings](../context/native-request-fields.md) for
first-party sources. Do not claim that screen membership or a Jira required flag
alone makes a field visible and usable on an existing customer request form.
