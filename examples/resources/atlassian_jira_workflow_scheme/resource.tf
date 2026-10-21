resource "atlassian_jira_workflow_scheme" "example" {
  name             = "Example Workflow Scheme"
  default_workflow = atlassian_jira_workflow.example.name

  # When Jira requires migrations, supply explicit destinations by issue type:
  # status_mappings = {
  #   "10001" = { "10010" = "10020" }
  # }
}
