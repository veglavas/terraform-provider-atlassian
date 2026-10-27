resource "atlassian_jira_project_field_scheme" "managed" {
  project_id = "10002"
  scheme_id  = atlassian_jira_field_scheme.managed.id

  # Explicit fallback on destroy. Omit to retain the remote assignment.
  on_destroy_scheme_id = "90000"
}
