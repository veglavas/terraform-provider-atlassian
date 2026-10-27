terraform {
  required_providers {
    atlassian = {
      source = "registry.terraform.io/veglavas/atlassian"
      # Requires a provider build containing field-scheme requirement support.
    }
  }
}

# Authentication uses ATLASSIAN_URL, ATLASSIAN_USER and ATLASSIAN_TOKEN.
provider "atlassian" {}

variable "project_id" {
  type        = string
  description = "Existing project whose field association scheme is discovered."
}

variable "requirements" {
  description = "Readable keys such as 'Support / Department'. Fields must already be associated with the scheme and available to the selected issue types."
  type = map(object({
    field_id      = string
    issue_type_id = string
    is_required   = bool
  }))
}

# Read-only: this does not create or assign a scheme.
data "atlassian_jira_project_field_scheme" "existing" {
  project_id = var.project_id
}

# WARNING: changes affect every project sharing this scheme.
resource "atlassian_jira_field_scheme_requirement" "selected" {
  for_each = var.requirements

  scheme_id     = data.atlassian_jira_project_field_scheme.existing.id
  field_id      = each.value.field_id
  issue_type_id = each.value.issue_type_id
  is_required   = each.value.is_required
}

output "scheme" {
  value = data.atlassian_jira_project_field_scheme.existing
}
