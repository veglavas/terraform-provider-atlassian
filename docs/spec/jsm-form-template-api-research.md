# JSM Forms template automation: public API findings

## Sources

- [Atlassian Forms REST API](https://developer.atlassian.com/cloud/forms/rest/)
- [Official Forms OpenAPI specification](https://developer.atlassian.com/cloud/forms/swagger.v3.json)
- [Customize request-type fields](https://support.atlassian.com/jira-service-management-cloud/docs/customize-the-fields-of-a-request-type/)
- [Link a form field to a Jira field](https://support.atlassian.com/jira-service-management-cloud/docs/link-a-form-field-to-a-jira-field/)

## Confirmed public capabilities

The Forms OpenAPI specification exposes:

- `POST /project/{projectIdOrKey}/form`: create a project form template.
- `GET`, `PUT`, and `DELETE /project/{projectIdOrKey}/form/{formId}`: read, save, and delete a template.
- Both create and save accept `FormTemplateRequest`.
- `FormTemplate` contains `design` and `publish`.
- `FormPublishing.portal` uses `FormPublishingPortal`, whose `portalRequestTypeIds` selects request types that display the form in the portal. It also controls `submitOnCreate` and `validateOnCreate`.
- `FormQuestion.jiraField` is documented as the linked Jira field.
- Question types include short text, date, and cascading dropdown, among others.

These are project Forms templates, not the native request-type field configuration. They establish a documented route to create forms and associate them with request types, but do not establish that every Jira field can be linked or that native request-form fields can be removed through the Forms API. Supported field mappings, choice synchronization, authentication, request schema, and layout must be checked before implementation.

Atlassian's native request-type customization documentation says mandatory system fields such as Summary cannot be removed or hidden. However, its field-linking documentation explicitly discusses a linked Summary question: required linked fields, including Summary, cannot be in conditional sections. It also says linking a field already on the associated request type removes it from that request type so it can appear in the form instead. This establishes a supported UI workflow for representing Summary as a linked question, not eliminating the underlying Summary requirement. Whether saving a template via the public API performs that native-field removal automatically remains unverified; do not promise a custom-fields-only portal until tested.

## Provider implications

No Forms-template resource has been implemented as part of this investigation. A provider feature could expose the documented template lifecycle and request-type publishing configuration, reusing the consumer's per-issue-type field selection where compatible.

This finding does not supply a public portal-group write endpoint. Portal-group assignment and Forms-template publishing are separate concerns.
