# Reuse a managed service project; the lookup also works when the project is
# created in the same apply. See examples/jsm-request-types for a full setup.
data "atlassian_jsm_service_desk" "example" {
  project_id = atlassian_jira_project.service.id
}

resource "atlassian_jsm_request_type" "example" {
  service_desk_id = data.atlassian_jsm_service_desk.example.id
  issue_type_id   = atlassian_jira_issue_type.joiner.id
  name            = "Employee onboarding"
  description     = "Request onboarding for a new employee."
  help_text       = "Provide the details needed for HR validation."

  # All configurable changes require replacement. Deleting a request type
  # removes it from existing customer requests.
  lifecycle {
    prevent_destroy = true
  }
}

# Newly created request types are hidden from portal browsing until grouped
# in Jira. Portal grouping and form layout are not managed by this resource.
