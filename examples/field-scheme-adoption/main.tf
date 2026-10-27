terraform {
  required_version = ">= 1.5"
  required_providers {
    atlassian = {
      source = "veglavas/atlassian"
      # Requires a build containing field-scheme management support.
    }
  }
}

# Uses ATLASSIAN_URL, ATLASSIAN_USER, and ATLASSIAN_TOKEN.
provider "atlassian" {}

variable "project_id" {
  type = string
}

variable "existing_scheme_id" {
  type        = string
  description = "Modern field scheme ID to import, not a guessed legacy configuration ID."
}

variable "scheme_name" {
  type        = string
  description = "Initially use the existing scheme's name to avoid an unintended rename."
}

variable "scheme_description" {
  type    = string
  default = ""
}

variable "fallback_scheme_id" {
  type        = string
  description = "Known existing scheme to assign if project-assignment ownership is destroyed."
}

variable "fields" {
  type        = map(set(string))
  description = "Field ID to complete issue-type restriction set. Empty means all issue types. Import each existing association before apply."
}

variable "requirements" {
  type = map(object({
    field_id      = string
    issue_type_id = string
    is_required   = bool
  }))
  default = {}
}

resource "atlassian_jira_field_scheme" "managed" {
  name        = var.scheme_name
  description = var.scheme_description

  lifecycle {
    prevent_destroy = true
  }
}

import {
  to = atlassian_jira_field_scheme.managed
  id = var.existing_scheme_id
}

resource "atlassian_jira_project_field_scheme" "managed" {
  project_id           = var.project_id
  scheme_id            = atlassian_jira_field_scheme.managed.id
  on_destroy_scheme_id = var.fallback_scheme_id
}

import {
  to = atlassian_jira_project_field_scheme.managed
  id = var.project_id
}

# Each resource owns only this field's association and full restriction set.
# Fields not declared here remain untouched.
resource "atlassian_jira_field_scheme_field" "managed" {
  for_each = var.fields

  scheme_id      = atlassian_jira_field_scheme.managed.id
  field_id       = each.key
  issue_type_ids = each.value
}

resource "atlassian_jira_field_scheme_requirement" "managed" {
  for_each = var.requirements

  scheme_id = atlassian_jira_field_scheme.managed.id
  field_id = atlassian_jira_field_scheme_field.managed[
    each.value.field_id
  ].field_id
  issue_type_id = each.value.issue_type_id
  is_required   = each.value.is_required
}
