resource "atlassian_jira_workflow" "example" {
  name        = "Example Workflow"
  description = "Description and transitions can be updated in place"
  statuses    = [atlassian_jira_status.example.id]

  transitions = {
    "1" = {
      name         = "Create"
      type         = "INITIAL"
      to_status_id = atlassian_jira_status.example.id
    }
    "11" = {
      name         = "Reopen"
      type         = "GLOBAL"
      to_status_id = atlassian_jira_status.example.id
    }
  }
}
