# Native request fields and Jira field requirements

## Provider scope

This is a generic Atlassian provider. Resources, tests, examples, and documentation must not encode a consuming deployment's project names, tenant configuration, resource addresses, or operational handovers. Keep deployment-specific configuration in its consuming repository and private session notes outside this checkout.

## Verified public API boundary

Native JSM request-type fields are distinct from Jira screens and Jira field constraints. Atlassian's native request-type configuration supports selecting and ordering fields, customer-facing labels/descriptions, required flags, and hidden preset values. These are not Atlassian Forms templates.

Adding a field to a Jira screen is documented to affect the agent work-item view. This does not establish that the field is selected on the customer request form. Mandatory system fields such as Summary cannot be removed or hidden.

Sources:

- [Customize a request type](https://support.atlassian.com/jira-service-management-cloud/docs/customize-the-fields-of-a-request-type/).
- [Hidden fields and unsupported fields](https://support.atlassian.com/jira-service-management-cloud/docs/what-are-hidden-fields-and-unsupported-fields-in-request-types/).

### Native request-field reads

The [official JSM OpenAPI](https://dac-static.atlassian.com/cloud/jira/service-desk/swagger.v3.json) defines:

```text
GET /rest/servicedeskapi/servicedesk/{serviceDeskId}/requesttype/{requestTypeId}/field
```

`RequestTypeFieldDTO` exposes `fieldId`, `name`, `description`, `required`, `visible`, `defaultValues`, `presetValues`, `validValues`, and `jiraSchema`. Hidden fields are visible only to service desk administrators; an administrator's response must not be treated as the customer's visible field list without checking `visible`.

No operation for writing native field selection, order, required flags, labels, or presets was found in this public contract. This is a statement about documented public API support, not about private endpoints used by Atlassian's UI. A provider must not invent a writable resource around the read-only contract.

A native request-field data source could expose existing settings without claiming to manage them. Its mock-backed tests should run without `TF_ACC`.

### Creation-time defaults are not ongoing synchronization

The public create-request-type operation states:

> Request type field mapping is set to show the required fields as specified by the issue type used to create the customer request type.

The request body accepts only `description`, `helpText`, `issueTypeId`, and `name`. This initial default is not evidence that later Jira field-configuration changes update existing request forms, nor does it manage optional field selection or ordering. Do not recreate existing request types merely to exploit this default; deletion removes the type from existing customer requests.

Source: [Create request type](https://developer.atlassian.com/cloud/jira/service-desk/rest/api-group-servicedesk/#api-rest-servicedeskapi-servicedesk-servicedeskid-requesttype-post).

## Jira field constraints: legacy and replacement APIs

The [official Jira OpenAPI](https://dac-static.atlassian.com/cloud/jira/platform/swagger-v3.v3.json) marks the legacy field-configuration and field-configuration-scheme operations deprecated and points to [Field schemes](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-field-schemes/).

The replacement supports per-issue-type requirements:

```text
PUT /rest/api/3/config/fieldschemes/fields/parameters
GET /rest/api/3/config/fieldschemes/{id}/fields/{fieldId}/parameters
```

`FieldsSchemeItemWorkTypeParameter.workTypeId` is explicitly documented as a work type (issue type) ID and supports `isRequired` overrides. This is a candidate for generic issue-type-specific field requirements, not a native portal-layout API.

Implementation caveats:

- Parameter operations are marked experimental in the inspected schema.
- Batch writes may return HTTP 207 partial success. Inspect individual results; treating every 2xx as complete success is insufficient.
- Removing an override is distinct from explicitly setting it false.
- The read-parameters endpoint does not document 404; do not infer parent deletion from its failure. Search responses may legally omit required parameters, so consult the parameter-read contract (`FieldAssociationParameters.isRequired` and `WorkTypeParameters.isRequired` are required when those objects are present). Neither endpoint defines an absent effective flag as false. If neither supplies an effective value, return a diagnostic and preserve state rather than invent a default.
- The field-list endpoint documents 404 for both a missing scheme and a disabled feature flag. Disambiguate via the direct scheme lookup before removing state. Project-discovery 404 explicitly means the experimental feature is disabled.
- A transitional project-list endpoint does not establish that company-managed projects cannot use the replacement API.
- API deprecation is verified; no removal deadline or tenant rollout status was established here.

If retaining legacy support, actual mapping deletion exists:

```text
POST /rest/api/3/fieldconfigurationscheme/{id}/mapping/delete
```

Do not simulate deletion by reassigning mappings to an instance default and filtering those real mappings from state. Preserve explicit fallback and issue-type entries for drift detection.

The legacy field-update documentation also warns that hiding a field removes its required/description/renderer values. Unhiding does not restore those values. Resetting a field to optional and visible on resource destruction must not be described as restoring its original state.

Sources:

- [Issue field configurations](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issue-field-configurations/).
- [Issue field configuration schemes](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issue-field-configuration-schemes/).

## Acceptance coverage boundary

This feature currently has always-on, mock-backed Terraform lifecycle coverage, not a live acceptance test. No live mutation was authorized and no dedicated disposable field-scheme fixture was available. Adding a test that mutates a caller's existing/shared scheme would be unsafe, especially because destroying the requirement restores inheritance rather than restoring a previous explicit override.

Before production validation, provision an isolated disposable scheme with a compatible associated field and issue type. A gated acceptance test must verify the initial fixture has no pre-existing required override, require explicit fixture IDs and the existing demo-tenant safety checks, and include a CheckDestroy that verifies only the test's override was removed. Verify effective requirements and untouched defaults/other issue types through API read-back. Do not substitute a permanently skipped test for that coverage or claim a live pass based on mocks.

These findings verify published contracts, not any particular tenant's settings or rendered UI. Live verification, when available and authorized, should read existing native field settings rather than infer them from screens or mutate request types.
