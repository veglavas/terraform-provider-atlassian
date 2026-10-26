data "atlassian_jira_custom_field_context" "example" {
  field_id = "customfield_10000"
  id       = "10100"
}

# Alternatively, specify an exact name instead of id.
# The lookup fails if more than one context has that name.
data "atlassian_jira_custom_field_context" "by_name" {
  field_id = "customfield_10000"
  name     = "Default Configuration Scheme for Department/Unit"
}
