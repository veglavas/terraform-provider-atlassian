# Import the current field order of a screen tab.
# ID format: {screen_id}/{tab_id}
#
# The imported field_ids reflect the remote order at import time; make sure the
# configuration lists the exact same membership before the next apply.
terraform import atlassian_jira_screen_tab_field_order.example 10001/20001
