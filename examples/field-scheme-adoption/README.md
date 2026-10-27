# Import and manage an existing field scheme

Requires a provider build containing field-scheme management support. These experimental API operations have local mock coverage, not live tenant validation.

The scheme is a resource, not a discovery-only data source. Ownership is split between scheme metadata, individual field associations, project assignment, and individual required overrides. Importing the scheme does **not** implicitly own or remove its fields or projects.

## Example inputs

Use the current name, description, restrictions, and flags for the initial adoption. All IDs below are illustrative:

```hcl
project_id         = "10002"
existing_scheme_id = "10000"
scheme_name        = "Example field scheme"
scheme_description = "Managed field scheme"
fallback_scheme_id = "90000"

fields = {
  customfield_10000 = ["10001", "10003"]
  customfield_10001 = [] # Available to all issue types in this scheme.
}

requirements = {
  department_for_task = {
    field_id      = "customfield_10000"
    issue_type_id = "10001"
    is_required   = true
  }
  department_for_bug = {
    field_id      = "customfield_10000"
    issue_type_id = "10003"
    is_required   = false
  }
}
```

## Adopt before changing

The configuration includes import blocks for the scheme and project assignment. Import each existing field association and required override as well, before applying changes:

```shell
terraform init
terraform import atlassian_jira_field_scheme.managed 10000
terraform import atlassian_jira_project_field_scheme.managed 10002
terraform import 'atlassian_jira_field_scheme_field.managed["customfield_10000"]' '10000:customfield_10000'
terraform import 'atlassian_jira_field_scheme_field.managed["customfield_10001"]' '10000:customfield_10001'
terraform import 'atlassian_jira_field_scheme_requirement.managed["department_for_task"]' '10000:customfield_10000:10001'
terraform import 'atlassian_jira_field_scheme_requirement.managed["department_for_bug"]' '10000:customfield_10000:10003'
terraform plan
```

The CLI sequence imports the parent scheme first so its ID is available to dependent configuration. The declarative import blocks become no-ops for resources already imported.

Review the initial plan carefully. If the project currently uses another scheme, the project resource will plan a reassignment. A shared scheme changes requirements for **every** project using it. Do not assume old field-configuration IDs identify modern field schemes.

## Deletion and scope

- The example protects the imported scheme with `prevent_destroy`.
- Jira rejects deletion of a system scheme or a scheme still assigned to projects.
- Jira has no project unassignment endpoint. `on_destroy_scheme_id` explicitly reassigns to the chosen existing fallback on destroy. If omitted, destroy retains the assignment and relinquishes Terraform ownership, which can block scheme deletion. No default ID is guessed.
- Removing a field-association resource removes the field from this scheme and can remove its parameters/required overrides. Review destructive plans; import does not retain a backup of previous restrictions or parameters.
- Removing a requirement resource deletes that issue-type override and restores inheritance. It does not necessarily make the field optional.
- Custom-field contexts, Jira create screens, and native JSM customer forms are separate configuration areas. This example does not select or reorder native portal fields.
