terraform {
  required_providers {
    atlassian = { source = "veglavas/atlassian" }
  }
}

provider "atlassian" {} # ATLASSIAN_URL, ATLASSIAN_USER, ATLASSIAN_TOKEN

variable "project_id" {
  type        = string
  description = "Existing JSM project ID or key. Prefer the stable numeric ID."
}

variable "forms" {
  description = "Exactly joiner, mover and leaver; existing source templates, request types and question-to-custom-field mappings."
  type = map(object({
    source_template_id = string
    request_type_id    = string
    field_links        = map(string) # question ID => existing customfield_NNNNN
  }))
  validation {
    condition     = toset(keys(var.forms)) == toset(["joiner", "mover", "leaver"])
    error_message = "Supply joiner, mover and leaver."
  }
}

data "atlassian_jsm_form_template" "source" {
  for_each   = var.forms
  project_id = var.project_id
  id         = each.value.source_template_id
}

locals {
  designs = { for name, source in data.atlassian_jsm_form_template.source : name => jsondecode(source.design) }
}

resource "atlassian_jsm_form_template" "jml" {
  for_each   = var.forms
  project_id = var.project_id
  design = jsonencode(merge(local.designs[each.key], {
    settings = merge(local.designs[each.key].settings, { name = title(each.key) })
    questions = merge(local.designs[each.key].questions, {
      for question_id, field_id in each.value.field_links : question_id => merge(
        local.designs[each.key].questions[question_id],
        { jiraField = field_id }
      )
    })
  }))
  publish = jsonencode({
    portal = {
      portalRequestTypeIds = [tonumber(each.value.request_type_id)]
      submitOnCreate       = true
      validateOnCreate     = true
    }
  })

  lifecycle {
    prevent_destroy = true
  }
}

output "form_ids" {
  value = { for name, form in atlassian_jsm_form_template.jml : name => form.id }
}
