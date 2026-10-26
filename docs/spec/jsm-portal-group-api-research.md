# JSM portal-group assignment: public REST API findings

## Scope

Checked the current Atlassian-hosted [JSM Cloud OpenAPI specification](https://dac-static.atlassian.com/cloud/jira/service-desk/swagger.v3.json). These findings concern the documented public JSM REST API, not every backend interface used by Atlassian's UI.

## Findings

- `POST /rest/servicedeskapi/servicedesk/{serviceDeskId}/requesttype` explicitly states: “Request type groups is left empty, which means this customer request type will not be visible on the customer portal.” It directs administrators to update these fields using Request types in Project settings.
- Its request schema, `RequestTypeCreateDTO`, permits only `description`, `helpText`, `issueTypeId`, and `name`, and sets `additionalProperties: false`. It does not accept `groupIds`.
- `RequestTypeDTO` includes `groupIds` in the response. This does not establish support for writing that attribute; the create request uses a different schema. The create response example includes a nonempty group ID despite the operation description saying groups are initially empty; that example is not evidence of an accepted input field.
- `/rest/servicedeskapi/servicedesk/{serviceDeskId}/requesttypegroup` exposes only GET.
- `/rest/servicedeskapi/servicedesk/{serviceDeskId}/requesttype/{requestTypeId}` exposes only GET and DELETE, not a metadata update operation.
- Request-type entity-property PUT endpoints exist, but the specification does not identify an entity property that configures portal-group membership. Their existence is not evidence that group membership can be updated through them.

All endpoint and schema claims above come from the linked OpenAPI specification.

## Conclusion

There is no documented portal-group assignment operation in the inspected public JSM REST contract. Do not broaden this conclusion to “no API exists”: private UI endpoints or other API surfaces have not been established or ruled out by this inspection. No tenant mutations or undocumented endpoint experiments were performed.

For the provider, supported group assignment remains unavailable through this public contract. UI configuration is the verified route described by the create operation; any private-interface workaround requires separate investigation and an explicit decision about its compatibility and support risks.
