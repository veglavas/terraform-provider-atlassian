data "atlassian_jira_field_scheme_requirement" "example" {
  scheme_id     = var.scheme_id
  field_id      = var.field_id
  issue_type_id = var.issue_type_id
}

output "is_required" {
  value = data.atlassian_jira_field_scheme_requirement.example.is_required
}
