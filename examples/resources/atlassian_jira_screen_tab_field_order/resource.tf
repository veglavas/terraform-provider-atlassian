# Field membership stays owned by atlassian_jira_screen_tab_field resources.
# This resource orders the fields that those resources place on the tab.
resource "atlassian_jira_screen" "example" {
  name = "Example Screen"
}

resource "atlassian_jira_screen_tab" "example" {
  screen_id = atlassian_jira_screen.example.id
  name      = "Details"
}

resource "atlassian_jira_screen_tab_field" "summary" {
  screen_id = atlassian_jira_screen.example.id
  tab_id    = atlassian_jira_screen_tab.example.id
  field_id  = "summary"
}

resource "atlassian_jira_screen_tab_field" "department" {
  screen_id = atlassian_jira_screen.example.id
  tab_id    = atlassian_jira_screen_tab.example.id
  field_id  = "customfield_10001"
}

# field_ids must list every field on the tab exactly once, in the desired
# order from first to last. depends_on guarantees all placements exist before
# the order is reconciled, and makes destroy relinquish ordering before the
# placements are removed.
resource "atlassian_jira_screen_tab_field_order" "example" {
  screen_id = atlassian_jira_screen.example.id
  tab_id    = atlassian_jira_screen_tab.example.id

  field_ids = [
    "summary",
    atlassian_jira_screen_tab_field.department.field_id,
  ]

  depends_on = [
    atlassian_jira_screen_tab_field.summary,
    atlassian_jira_screen_tab_field.department,
  ]
}
