# Publishing the fork

Provider identity: `registry.terraform.io/veglavas/atlassian`.
Repository: `https://github.com/veglavas/terraform-provider-atlassian`.

The repository must be public for Terraform Registry's public publishing flow.
The Go module path intentionally remains upstream's path: it is not the registry identity.

## One-time setup

1. Enable GitHub Actions in the fork.
2. Create a dedicated GPG signing key with a passphrase (for example, using `gpg --full-generate-key`). Export its private key and public key locally; never commit the private key or passphrase.
3. In the fork's **Settings → Secrets and variables → Actions**, create repository secrets:

   | Secret | Value |
   | --- | --- |
   | `GPG_PRIVATE_KEY` | ASCII-armored private signing key |
   | `GPG_PASSPHRASE` | Signing key passphrase |
   | `RELEASE_TOKEN` | Fine-grained GitHub PAT for this repository with Contents, Pull requests, and Issues read/write permissions |

   The PAT must belong to a user allowed to create release PRs and tags. Using the default `GITHUB_TOKEN` for Release Please would prevent its tag push from triggering the release workflow. Renew the PAT before expiry. Enable **Allow GitHub Actions to create and approve pull requests** in Actions settings if required by your repository policy.
4. Sign in to [Terraform Registry](https://registry.terraform.io/) using the GitHub account that owns the `veglavas` namespace. Add the GPG **public** signing key to that namespace's signing keys.

Useful local export commands (replace `FINGERPRINT` with your key's fingerprint):

```bash
gpg --list-secret-keys --keyid-format long
gpg --armor --export-secret-keys FINGERPRINT > signing-private.asc
gpg --armor --export FINGERPRINT > signing-public.asc
```

Store the private export securely outside this repository, upload it only as the GitHub secret, and remove the temporary private export afterward.

## First fork release: v0.1.3

The manifest records the last released version, `0.1.2`. Leave it unchanged until Release Please prepares the next release.

1. Push the prepared changes to the fork's `main` branch. Release Please opens or updates a release PR for `0.1.3`.
2. Check that the release PR proposes `0.1.3` and updates the manifest and changelog. Wait for CI and review to pass, then merge it. Release Please creates the `v0.1.3` tag automatically; do not create the tag manually.
3. Wait for **Release** in GitHub Actions to succeed. GoReleaser builds platform ZIPs, embeds the tag version in the binary, and uploads checksums, their GPG signature, and the protocol manifest to the GitHub release. No source change to `main.version` is needed.
4. In Terraform Registry, choose **Publish → Provider**, select `veglavas/terraform-provider-atlassian`, and complete registration. Confirm version `0.1.3` is indexed and installable before asking colleagues to migrate.

Do not move a published version tag. Fix a failed release before publishing, or release a new version if consumers have already installed it.

## Subsequent releases

Conventional commits merged to `main` cause **Release Please** to open or update a release PR. Merging it updates the manifest/changelog and creates a version tag. The PAT-created tag triggers **Release**, and Terraform Registry discovers the signed release via its GitHub integration.

Release signing uses repository secrets and does not require 1Password. The separate live integration-test workflow still uses the inherited 1Password integration; configure it with your own test tenant before running it. It is not needed to build or publish a release.

## Verify as a consumer

In a fresh directory, create `main.tf`:

```hcl
terraform {
  required_providers {
    atlassian = {
      source  = "registry.terraform.io/veglavas/atlassian"
      version = "= 0.1.3"
    }
  }
}
```

Run `terraform init` (or `tofu init`) without development overrides. Confirm the installed provider is `veglavas/atlassian v0.1.3` and its signature is verified. Commit each consuming project's lock file. See the repository README for migration of existing state from the upstream provider.
