resource "atlassian_jira_project_workflow_scheme" "example" {
  project_id         = atlassian_jira_project.example.id
  workflow_scheme_id = atlassian_jira_workflow_scheme.example.id

  # Populated projects are supported. Explicit migration destinations, if needed:
  # status_mappings = {
  #   "10001" = { "10010" = "10020" }
  # }

  # Optional: explicitly reassign on destroy. Otherwise retain the association.
  # delete_workflow_scheme_id = "your-fallback-scheme-id"
  # delete_status_mappings = {
  #   "10001" = { "10020" = "10010" }
  # }
}
