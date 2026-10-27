resource "atlassian_jira_field_scheme" "managed" {
  name        = "Example field scheme"
  description = "Managed by Terraform"

  lifecycle {
    prevent_destroy = true
  }
}
