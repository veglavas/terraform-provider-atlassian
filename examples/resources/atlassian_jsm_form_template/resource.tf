# Reuse a valid existing design through the documented GET endpoint.
# The public OpenAPI layout example is only [{}]; it cannot bootstrap a
# useful question layout. See docs/spec/jsm-form-templates.md.
variable "project_id" { type = string }
variable "source_template_id" { type = string }
variable "request_type_id" { type = string }
variable "employee_question_id" { type = string }
variable "employee_custom_field_id" { type = string }

data "atlassian_jsm_form_template" "source" {
  project_id = var.project_id
  id         = var.source_template_id
}

locals {
  source_design = jsondecode(data.atlassian_jsm_form_template.source.design)
}

resource "atlassian_jsm_form_template" "joiner" {
  project_id = var.project_id
  design = jsonencode(merge(local.source_design, {
    settings = merge(local.source_design.settings, { name = "Joiner" })
    questions = merge(local.source_design.questions, {
      (var.employee_question_id) = merge(local.source_design.questions[var.employee_question_id], {
        # Existing compatible Jira field; preserve the source question and layout.
        jiraField = var.employee_custom_field_id # e.g. customfield_10042
      })
    })
  }))
  publish = jsonencode({
    portal = {
      portalRequestTypeIds = [tonumber(var.request_type_id)]
      submitOnCreate       = true
      validateOnCreate     = true
    }
  })
}
