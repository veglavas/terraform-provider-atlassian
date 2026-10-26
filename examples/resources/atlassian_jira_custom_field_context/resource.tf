resource "atlassian_jira_custom_field_context" "example" {
  field_id       = "customfield_10000"
  name           = "Project-specific choices"
  description    = "Choices for the selected project"
  project_ids    = ["10001"]
  issue_type_ids = [] # All issue types within the selected project.

  lifecycle {
    prevent_destroy = true
  }
}
