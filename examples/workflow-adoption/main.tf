terraform {
  required_providers {
    atlassian = {
      source  = "registry.terraform.io/veglavas/atlassian"
      version = "~> 0.1.3"
    }
  }
}

# Authentication comes from ATLASSIAN_URL, ATLASSIAN_USER, ATLASSIAN_TOKEN.
provider "atlassian" {}

variable "workflow_id" { type = string }
variable "workflow_name" { type = string }
variable "workflow_scheme_id" { type = string }
variable "workflow_scheme_name" { type = string }
variable "project_id" { type = string }
variable "status_ids" { type = list(string) }
variable "description" {
  type    = string
  default = "Workflow managed in place with Terraform"
}
variable "transitions" {
  type = map(object({
    name            = string
    type            = string
    to_status_id    = string
    from_status_ids = optional(list(string))
  }))
  default = null
}
variable "workflow_status_mappings" {
  type    = map(string)
  default = null
}
variable "scheme_status_mappings" {
  type    = map(map(string))
  default = null
}

import {
  to = atlassian_jira_workflow.managed
  id = var.workflow_id
}
import {
  to = atlassian_jira_workflow_scheme.managed
  id = var.workflow_scheme_id
}
import {
  to = atlassian_jira_project_workflow_scheme.managed
  id = var.project_id
}

resource "atlassian_jira_workflow" "managed" {
  name            = var.workflow_name
  description     = var.description
  statuses        = var.status_ids
  transitions     = var.transitions
  status_mappings = var.workflow_status_mappings
  lifecycle { prevent_destroy = true }
}

resource "atlassian_jira_workflow_scheme" "managed" {
  name             = var.workflow_scheme_name
  default_workflow = atlassian_jira_workflow.managed.name
  status_mappings  = var.scheme_status_mappings
  # Omitted issue_type_mappings are read from Jira and preserved.
  lifecycle { prevent_destroy = true }
}

resource "atlassian_jira_project_workflow_scheme" "managed" {
  project_id         = var.project_id
  workflow_scheme_id = atlassian_jira_workflow_scheme.managed.id
  status_mappings    = var.scheme_status_mappings
  lifecycle { prevent_destroy = true }
}
