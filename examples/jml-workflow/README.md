# In-place JML workflow management

This example adopts an existing **company-managed** Jira project, workflow, and
workflow scheme. It does not create temporary workflows or unassign projects.
Use a disposable project first. The provider build containing these changes must
be installed (for example using a Terraform CLI `dev_overrides` entry).

1. Supply `ATLASSIAN_URL`, `ATLASSIAN_USER`, and `ATLASSIAN_TOKEN` securely.
2. Supply your real workflow UUID, workflow name, scheme ID/name, project ID,
   and existing status IDs in `terraform.tfvars`. Do not guess IDs from examples.
3. Keep `transitions = null` initially to preserve all existing transitions.
4. Run `terraform plan`. The import blocks adopt the existing objects; inspect
   every proposed change, especially the scheme's default workflow.
5. Apply. Then change `description` and apply again. The workflow UUID must remain
   unchanged, and the plan must show an update, not replacement.
6. To manage a transition, add it to `transitions` using its **existing transition
   ID**, for example:

   ```hcl
   transitions = {
     "41" = {
       name            = "Start mover process"
       type            = "DIRECTED"
       from_status_ids = ["YOUR_SOURCE_STATUS_ID"]
       to_status_id    = "YOUR_DESTINATION_STATUS_ID"
     }
   }
   ```

   Replace `41` and the status IDs with actual IDs from your workflow. Existing
   validators, conditions, post-functions, triggers, and properties are retained.
   Transitions not listed remain unmanaged. Removing a previously managed map
   entry deletes that transition; setting the entire attribute to null relinquishes
   management without deleting transitions.

When removing a status, explicitly update affected transitions and supply
`workflow_status_mappings = { OLD_STATUS_ID = NEW_STATUS_ID }` if issues need
migration. This mapping applies to **all projects and issue types using that
workflow**. Scheme or project switches instead use
`scheme_status_mappings = { ISSUE_TYPE_ID = { OLD_STATUS_ID = NEW_STATUS_ID } }`.
The provider never chooses a migration destination for you.

The lifecycle guards intentionally prevent accidental destruction, including a
workflow rename (Jira's documented update API does not support rename). Back up
Terraform state before adoption. This example has not been live-verified against
your Jira tenant; permissions, workflow rules and scheme constraints can differ.
