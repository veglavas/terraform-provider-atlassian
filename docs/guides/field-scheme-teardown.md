# Field scheme teardown: current limitation

## Reported behavior

A custom field replacement can be blocked when deleting its issue-type requirement overrides returns HTTP 400. Manually removing the field from its scheme in Jira, then applying again, allows replacement to proceed.

This does **not** establish that the parameter DELETE succeeds after detachment. Removing the association clears its overrides. The provider reads that the association is absent and completes requirement deletion without sending the parameter DELETE.

The underlying reason for the parameter endpoint's rejection remains unknown. The published request contract matches the current payload. This investigation does not establish that individual override deletion always fails or always succeeds on a live tenant.

## Terraform lifecycle reproduction

Run without Jira credentials or `TF_ACC`:

```sh
go test ./internal/jira -run '^TestFieldSchemeTeardownDependencyLifecycle$' -count=1 -v
```

The mock models the reported rejection while exercising real Terraform planning and apply. It manages a scheme, a field association, and two requirement resources. A `terraform_data` output represents a changing field ID, isolating association replacement from unrelated custom-field HTTP operations.

The test verifies:

1. When parameter deletion is accepted, removing one requirement preserves the association and its sibling override.
2. When parameter deletion is rejected, changing the field ID fails before the association's DELETE is called.
3. Simulating manual association removal lets the next apply recreate the association and its requirements.
4. Destroying the entire configuration, including the managed scheme, encounters the same ordering problem.
5. Simulating detachment again lets scheme destruction complete.

The rejected writes are intentionally asserted with `ExpectError`. A passing test reproduces the limitation; it is **not** a fix or proof of the live Jira endpoint's behavior. The mock's rejection switch represents the observed failure condition, not knowledge that Jira distinguishes a replacement from standalone override deletion.

## Why managing the scheme does not coordinate deletion

References establish this creation order:

```text
scheme -> association -> requirements
```

Terraform reverses those dependencies for destruction:

```text
requirements -> association -> scheme
```

Each requirement owns one override, while the association resource owns the whole association. Deleting the association clears its scheme parameters and all issue-type overrides. A requirement must not detach it merely because that requirement is being removed.

In terraform-plugin-framework v1.19.0:

- `resource.ModifyPlanRequest` contains the individual resource's config, state, plan, and private data. It does not expose other resources' planned changes.
- `resource.DeleteRequest` contains the individual resource's state, identity, provider metadata, and private data. It does not expose the full plan or dependency graph.
- Per-resource private state cannot convey a separate association resource's teardown intent.

A shared in-memory planning registry would not solve this reliably: saved plans may be applied by a different provider process, provider aliases can use separate instances, and RPC order is not a synchronization contract. Mutating Jira during planning is not acceptable either.

## Proposed interface direction, not implemented

An opt-in requirement map on the field-association resource would let a single owner manage association creation, drift, requirement updates, and teardown. Deleting/replacing that resource could detach the association directly, rather than first deleting each override through separate dependent resources.

Existing standalone requirement resources would remain available. They must not manage the same overrides as the association's inline map. Adopting inline ownership would require explicitly transferring ownership out of their state without deleting the remote overrides; it cannot be a transparent schema upgrade that merges several resource instances into another.

Before implementing this direction, agree the configuration and state-migration contract. Tests must cover saved plans, import/adoption, incomplete reads, partial writes, standalone requirement isolation, field replacement, and association/scheme destruction. Inline ownership would address coordinated teardown, but must not be presented as a solution to an unexplained failure of the parameter endpoint during a standalone override update/removal.
