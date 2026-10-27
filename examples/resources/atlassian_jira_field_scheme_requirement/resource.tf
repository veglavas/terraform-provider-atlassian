# Discover the existing scheme; it may be shared with other projects.
data "atlassian_jira_project_field_scheme" "example" {
  project_id = var.project_id
}

# The field must already be associated and available to this issue type.
resource "atlassian_jira_field_scheme_requirement" "example" {
  scheme_id     = data.atlassian_jira_project_field_scheme.example.id
  field_id      = var.field_id
  issue_type_id = var.issue_type_id
  is_required   = true
}

# Destroy restores inheritance from the scheme default, not an earlier override.
# Native JSM request-form visibility and ordering are not managed here.
