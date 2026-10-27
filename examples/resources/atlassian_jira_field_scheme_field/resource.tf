resource "atlassian_jira_field_scheme_field" "managed" {
  scheme_id      = atlassian_jira_field_scheme.managed.id
  field_id       = "customfield_10000"
  issue_type_ids = ["10001", "10002"]
}
