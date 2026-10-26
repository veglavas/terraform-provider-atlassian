# Reference your existing Terraform-managed service project.
data "atlassian_jsm_service_desk" "example" {
  project_id = atlassian_jira_project.service.id
}

# Pass data.atlassian_jsm_service_desk.example.id to request types'
# service_desk_id. No manually discovered service desk ID is required.
