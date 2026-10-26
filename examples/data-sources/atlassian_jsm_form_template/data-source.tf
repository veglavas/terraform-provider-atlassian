data "atlassian_jsm_form_template" "joiner" {
  project_id = "10001"
  id         = "c18bde7a-d846-11ed-afa1-0242ac120002"
}

output "joiner_design" {
  value = jsondecode(data.atlassian_jsm_form_template.joiner.design)
}
