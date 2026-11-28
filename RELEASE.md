# Publishing pubg/jenkins

This repository publishes `registry.terraform.io/pubg/jenkins` from
[`pubg/terraform-provider-jenkins`](https://github.com/pubg/terraform-provider-jenkins).
The Go module is `github.com/pubg/terraform-provider-jenkins`; the provider type
and resource prefix remain `jenkins` and `jenkins_`.

## One-time setup

1. Enable GitHub Actions for this repository. Release workflows are restricted
   to this exact repository, including while GitHub classifies it as a fork.
2. Choose a PUBG-managed RSA GPG signing key and register its ASCII-armored
   **public** key under the Terraform Registry's `pubg` namespace. Keep the
   private key in the team's secret store. Do not reuse upstream credentials.
3. Configure these repository or organization Actions credentials, granting
   this repository access when using organization secrets:

   | Name | Kind | Purpose |
   | --- | --- | --- |
   | `GPG_PRIVATE_KEY` | Secret | ASCII-armored private release signing key |
   | `PASSPHRASE` | Secret | Passphrase for that key |

   No GitHub App or personal token is needed. `release.yml` publishes with the
   built-in `GITHUB_TOKEN` (`contents: write`), as HashiCorp's provider
   scaffolding does.
4. After the first signed GitHub release exists, sign in to the Terraform
   Registry with access to the `pubg` organization, select **Publish > Provider**,
   and choose this repository. Verify the signing key and repository webhook.
   Later GitHub releases are discovered through the webhook.

The Registry setup is separate from committing this configuration. A successful
snapshot build does not mean the provider is listed or its signing key is registered.
See HashiCorp's [publishing requirements](https://developer.hashicorp.com/terraform/registry/providers/publishing).

## Version and release flow

Releases follow HashiCorp's
[provider scaffolding](https://github.com/hashicorp/terraform-provider-scaffolding-framework):
a maintainer pushes a `vX.Y.Z` tag and `release.yml` publishes it. The first
PUBG release was **1.3.0**, on top of the upstream 1.2.8 baseline. Do not push
inherited upstream tags as PUBG releases.

1. Merge reviewed changes to `main` and wait for the `CI` workflow to succeed.
2. Pick the next [SemVer](https://semver.org/) version from the Conventional
   Commits since the last tag: `feat:` increments minor; `fix:` increments
   patch; breaking changes (`!` or `BREAKING CHANGE:`) increment major.
3. Open a pull request titled `chore: release vX.Y.Z` that adds a
   `## [X.Y.Z](https://github.com/pubg/terraform-provider-jenkins/compare/vPREV...vX.Y.Z) (YYYY-MM-DD)`
   section to the top of `CHANGELOG.md`, in the existing format, and merge it.
4. Tag the merged commit on `main` and push the tag:

   ```sh
   git switch main && git pull --ff-only
   git tag vX.Y.Z
   git push origin vX.Y.Z
   ```

5. `release.yml` builds the provider using GoReleaser, signs its checksums, and
   creates the GitHub Release with the platform ZIPs, manifest, checksums and
   detached signature. GoReleaser does not write release notes;
   `CHANGELOG.md` is the record of changes.
6. Check the release assets and Terraform Registry version, then test installation
   from a fresh directory without `dev_overrides`:

   ```hcl
   terraform {
     required_providers {
       jenkins = {
         source  = "pubg/jenkins"
         version = "1.3.0"
       }
     }
   }
   ```

   ```sh
   terraform init
   terraform providers schema -json
   ```

No Terraform Registry API token is needed by this workflow. The Registry reads
signed GitHub release assets after its one-time connection is configured.
OpenTofu requires separate registry onboarding; its availability is not implied
by publishing to the Terraform Registry.

## Validation and recovery

CI runs an unsigned GoReleaser snapshot build without publishing. It checks the
same platform packaging configuration without requiring release secrets:

```sh
goreleaser check
goreleaser release --snapshot --clean --skip=publish,sign
```

The manifest advertises only protocol **6.0**, which this Framework provider
serves. The ZIP and checksum names remain `terraform-provider-jenkins_*`.
The release version is embedded in the provider metadata at build time.

If CI fails, fix or rerun it before tagging. If signing or asset upload fails, fix the credentials and rerun the
failed Release job for the same tag before treating the release as complete.
Do not replace the assets of a version already indexed by the Registry; publish
a new version instead. Use the Registry's resync action when a completed release
is not discovered, and check its webhook deliveries.
