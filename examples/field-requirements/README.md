# Per-issue-type field requirements

This example discovers a project's **existing** field association scheme and
manages selected required flags by field and issue type. Use a local provider
build containing this feature; no released version is promised by this example.
The experimental API's availability has not been live-verified.

Supply `project_id` and a `requirements` map. Use readable map keys such as
`"Support / Department"`. Values are `field_id`, `issue_type_id`, and
`is_required`. Obtain real IDs from existing managed resources or data sources,
not from placeholder numeric IDs in documentation.

Before applying:

- Ensure the field is already associated with the scheme and available to the
  issue type. This feature does not add associations or unhide fields.
- Check all projects using the scheme: changing a shared scheme affects them all.
- Arrange the Jira create screen and field context so users can supply values.
- The API user needs the Administer Jira global permission.
- Do not manage the same scheme/field/issue-type tuple in multiple resources.

Changing `is_required` updates in place. Removing an entry from `requirements`
removes only that issue type's `isRequired` override, **restoring inheritance from
the scheme default**. It does not force the field optional or restore an older
explicit override. Other field parameters and issue-type overrides are untouched.
No default or subtask requirements are changed unless explicitly targeted.

This does **not** manage native JSM request-form field selection, order, labels,
or presets. Required Jira fields must be available to the person creating the
request or have a valid value source. Do not assume an existing portal request
form is updated by changing a Jira requirement.

See the [field requirements guide](../../docs/guides/field-requirements.md).
