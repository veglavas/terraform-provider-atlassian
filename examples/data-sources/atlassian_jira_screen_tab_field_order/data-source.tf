# Read the current field order of a screen tab (Jira and agent-side JSM
# Create/Edit screens; not the native JSM customer-portal request form).
data "atlassian_jira_screen_tab_field_order" "example" {
  screen_id = "10001"
  tab_id    = "20001"
}

# The remote order is exposed verbatim, first to last:
# data.atlassian_jira_screen_tab_field_order.example.field_ids
