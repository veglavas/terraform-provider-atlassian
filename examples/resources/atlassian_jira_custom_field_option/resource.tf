resource "atlassian_jira_custom_field_option" "parent" {
  field_id   = "customfield_10000"
  context_id = "10100"
  value      = "Department A"
}

# Omit parent_option_id for ordinary select fields and top-level options.
resource "atlassian_jira_custom_field_option" "child" {
  field_id         = atlassian_jira_custom_field_option.parent.field_id
  context_id       = atlassian_jira_custom_field_option.parent.context_id
  parent_option_id = atlassian_jira_custom_field_option.parent.id
  value            = "Unit A1"
  disabled         = false
}
