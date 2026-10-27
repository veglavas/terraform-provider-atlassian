data "atlassian_jira_project_field_scheme" "example" {
  project_id = var.project_id
}

output "field_scheme_id" {
  value = data.atlassian_jira_project_field_scheme.example.id
}
