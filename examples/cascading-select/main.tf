terraform {
  required_providers {
    atlassian = {
      source = "veglavas/atlassian"
    }
  }
}

provider "atlassian" {} # ATLASSIAN_URL, ATLASSIAN_USER, ATLASSIAN_TOKEN

variable "project_id" {
  description = "Project that uses this context."
  type        = string
}

variable "department_units" {
  description = "Departments and their units. Empty lists allow a department without units."
  type        = map(list(string))

  validation {
    condition = alltrue([
      for department, units in var.department_units :
      trimspace(department) != "" &&
      alltrue([for unit in units : trimspace(unit) != ""]) &&
      length(units) == length(distinct(units))
    ])
    error_message = "Names must not be blank and unit names must be unique within each department."
  }
}

resource "atlassian_jira_custom_field" "department_unit" {
  name = "Department/Unit"
  type = "com.atlassian.jira.plugin.system.customfieldtypes:cascadingselect"
}

# Jira may also create a global default context with the field. This resource
# neither adopts nor deletes it. See docs/guides/custom-field-options.md.
resource "atlassian_jira_custom_field_context" "department_unit" {
  field_id    = atlassian_jira_custom_field.department_unit.id
  name        = "Department/Unit choices"
  project_ids = [var.project_id]

  lifecycle {
    prevent_destroy = true
  }
}

resource "atlassian_jira_custom_field_option" "department" {
  for_each = var.department_units

  field_id   = atlassian_jira_custom_field_context.department_unit.field_id
  context_id = atlassian_jira_custom_field_context.department_unit.id
  value      = each.key
}

locals {
  units = flatten([
    for department, units in var.department_units : [
      for unit in units : {
        department = department
        unit       = unit
      }
    ]
  ])
}

resource "atlassian_jira_custom_field_option" "unit" {
  # JSON tuple keys avoid collisions when department/unit names contain separators.
  for_each = { for entry in local.units : jsonencode([entry.department, entry.unit]) => entry }

  field_id         = atlassian_jira_custom_field_context.department_unit.field_id
  context_id       = atlassian_jira_custom_field_context.department_unit.id
  parent_option_id = atlassian_jira_custom_field_option.department[each.value.department].id
  value            = each.value.unit
}

output "department_option_ids" {
  value = { for name, option in atlassian_jira_custom_field_option.department : name => option.id }
}

output "unit_option_ids" {
  value = { for key, option in atlassian_jira_custom_field_option.unit : key => option.id }
}
