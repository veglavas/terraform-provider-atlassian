resource "atlassian_jsm_request_type" "example" {
  service_desk_id = "2"     # JSM service desk ID, not the Jira project ID.
  issue_type_id   = "10001" # Existing parent issue type in this project.
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
