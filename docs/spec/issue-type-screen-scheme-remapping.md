# Issue-type screen-scheme remapping

## Bug and public contract

Changing the screen scheme associated with an existing issue type failed with Jira's HTTP 400: “One or more issue type IDs are already mapped, an issue type ID can only be mapped once.” The provider treated the mapping endpoint as an upsert.

The official [Jira Cloud OpenAPI specification](https://dac-static.atlassian.com/cloud/jira/platform/swagger-v3.v3.json) defines three separate operations:

- `PUT /rest/api/3/issuetypescreenscheme/{issueTypeScreenSchemeId}/mapping`: **Append mappings**. Existing mappings must not be included.
- `POST /rest/api/3/issuetypescreenscheme/{issueTypeScreenSchemeId}/mapping/remove`: remove explicit issue-type mappings using `issueTypeIds`.
- `PUT /rest/api/3/issuetypescreenscheme/{issueTypeScreenSchemeId}/mapping/default`: update the fallback using `screenSchemeId`.

## Update sequence

1. Update the default through its dedicated endpoint, if changed.
2. Remove explicit mappings that are deleted or whose target changes.
3. Append new mappings and replacements.
4. Leave unchanged mappings untouched and persist the desired state only after success.

The default is never sent to append/remove operations. Requests use stable issue-type ordering for deterministic tests and debugging.

These operations are not atomic. During replacement, affected issue types temporarily use the default screen scheme. If appending fails after removal succeeds, a subsequent Terraform refresh reads the partial remote result; retrying applies the missing mappings. No scheme or project association is destroyed to perform a remap.

## Verification

`TestIssueTypeScreenSchemeMappingUpdates` reproduces the user's three-mapping failure with a strict HTTP mock and covers default updates, combined add/remove/replace with unchanged mappings, failure at each mutation stage, refresh/retry, and no-op mapping updates.

The existing mock-backed Terraform lifecycle test also changes an existing mapping and the default. Its mock now rejects duplicate appends rather than silently accepting the provider's former upsert behavior.
