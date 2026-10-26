# Three JML portal templates

This configuration manages Joiner, Mover and Leaver Forms templates, linking
existing compatible Jira custom fields with `jiraField` and associating existing
request types through `publish.portal.portalRequestTypeIds`. It requires the
feature build of this provider. No UI actions are part of the configuration.

Supply `project_id` and a `forms` map with exactly `joiner`, `mover`, `leaver`.
Each entry contains an existing `source_template_id`, an existing
`request_type_id`, and `field_links` mapping **existing source question IDs** to
Jira IDs such as `customfield_10042`. Preserve the source question type and ensure
it matches its linked Jira field. Source designs must be suitable for their target
request types, with no unintended links to other fields.

The data sources retrieve valid designs including their layouts. The resource
copies each design and changes only its name and the supplied field links, then
publishes it on the corresponding request type. Stable `for_each` keys allow
project/request-type/custom-field resource outputs to be passed by a calling
module; unknown values defer until apply. Request IDs become JSON numbers via
`tonumber`. Use a numeric project ID to survive project-key changes.

**Bootstrap limitation:** the official OpenAPI example provides `layout: [{}]`,
not a usable question layout. This example therefore requires existing reusable
templates. You can instead supply a previously retrieved valid design JSON file
using `jsondecode(file(...))`. We do not provide invented ADF widgets or claim
three new useful forms can be bootstrapped solely from the published example.
Source templates must differ from the managed target templates. To manage existing
targets directly, import their project/UUID and supply their design.

This manages Forms only. Native request-type fields, Summary requirements, portal
groups/visibility, issue-type screens and Jira option synchronization are separate.
It does not promise a portal containing only custom fields. `prevent_destroy`
protects these example templates; removing it is a separate deliberate choice.
See [the contract and limitations](../../docs/spec/jsm-form-templates.md).
