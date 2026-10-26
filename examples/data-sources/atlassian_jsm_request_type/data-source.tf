data "atlassian_jsm_service_desk" "example" {
  project_id = atlassian_jira_project.service.id
}

data "atlassian_jsm_request_type" "example" {
  service_desk_id = data.atlassian_jsm_service_desk.example.id
  id              = "10"
}
