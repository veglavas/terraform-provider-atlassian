terraform {
  required_providers {
    atlassian = {
      source = "veglavas/atlassian"
    }
  }
}

provider "atlassian" {} # ATLASSIAN_URL, ATLASSIAN_USER, ATLASSIAN_TOKEN

variable "project_key" {
  description = "Key for a NEW company-managed service project."
  type        = string
}

variable "project_name" {
  description = "Name for the new project."
  type        = string
}

variable "lead_account_id" {
  description = "Atlassian accountId of the project lead."
  type        = string
}

resource "atlassian_jira_project" "service" {
  key              = var.project_key
  name             = var.project_name
  project_type_key = "service_desk"
  lead_account_id  = var.lead_account_id

  lifecycle {
    prevent_destroy = true
  }
}

# Create a parent issue type and assign it to the project's issue type scheme.
# For an existing project, reuse its existing issue types and scheme instead.
resource "atlassian_jira_issue_type" "request" {
  name = "${var.project_name} Request"
  type = "standard"
}

resource "atlassian_jira_issue_type_scheme" "service" {
  name                  = "${var.project_name} issue types"
  default_issue_type_id = atlassian_jira_issue_type.request.id
  issue_type_ids        = [atlassian_jira_issue_type.request.id]
}

resource "atlassian_jira_project_issue_type_scheme" "service" {
  project_id           = atlassian_jira_project.service.id
  issue_type_scheme_id = atlassian_jira_issue_type_scheme.service.id
}

# Terraform defers lookup until the managed project's generated ID is known.
# The provider discovers the service desk ID; no manual ID discovery required.
data "atlassian_jsm_service_desk" "service" {
  project_id = atlassian_jira_project.service.id
}

resource "atlassian_jsm_request_type" "request" {
  service_desk_id = data.atlassian_jsm_service_desk.service.id
  issue_type_id   = atlassian_jira_issue_type.request.id
  name            = "Submit a request"
  description     = "Submit a request to the service team."

  # The issue type must be available in the project before JSM can use it.
  depends_on = [atlassian_jira_project_issue_type_scheme.service]

  lifecycle {
    prevent_destroy = true
  }
}

# Remaining manual setup: portal grouping/visibility and request form layout.
# Creating these objects alone does not configure portal access or notifications.

output "service_desk_id" {
  value = data.atlassian_jsm_service_desk.service.id
}

output "request_type_id" {
  value = atlassian_jsm_request_type.request.id
}
