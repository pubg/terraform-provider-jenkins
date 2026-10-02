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
   | `APP_CLIENT_ID` | Secret or variable | Client ID of the release GitHub App; a secret takes precedence |
   | `APP_PRIVATE_KEY` | Secret | PEM private key for that App |

4. Install the GitHub App on this repository with **Contents: read/write** and
   **Pull requests: read/write**. Release-please uses its token so its PRs and
   tags trigger CI and the release workflow. The default `GITHUB_TOKEN` does
   not trigger those downstream workflows.
5. After the first signed GitHub release exists, sign in to the Terraform
   Registry with access to the `pubg` organization, select **Publish > Provider**,
   and choose this repository. Verify the signing key and repository webhook.
   Later GitHub releases are discovered through the webhook.

The Registry setup is separate from committing this configuration. A successful
snapshot build does not mean the provider is listed or its signing key is registered.
See HashiCorp's [publishing requirements](https://developer.hashicorp.com/terraform/registry/providers/publishing).

## Version and release flow

The inherited version baseline remains **1.2.8** in
`.release-please-manifest.json`. The first PUBG release is **1.3.0** because it
adds Config File Provider resources. `bootstrap-sha` points at the upstream
1.2.8 commit, so the first release notes include only subsequent fork changes.
After the first release, release-please uses the published release as its baseline.
Do not push inherited upstream tags as PUBG releases.

1. Merge a reviewed change to `main` and wait for the `CI` workflow to succeed.
2. `versioning.yml` uses release-please to open or update a Release PR with the
   next version and changelog. `feat:` increments minor; `fix:` increments patch;
   breaking changes increment major.
3. Review and merge the Release PR when ready to publish. This creates its
   `vX.Y.Z` tag and GitHub release notes.
4. `release.yml` builds the provider using GoReleaser, signs its checksums, and
   uploads the platform ZIPs, manifest, checksums and detached signature.
5. Check the release assets and Terraform Registry version, then test installation
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

If CI fails, fix or rerun it before releasing. If versioning misses a completed
CI run, use **Actions > Versioning > Run workflow** after confirming `main` is
green. If signing or asset upload fails, fix the credentials and rerun the
failed Release job for the same tag before treating the release as complete.
Do not replace the assets of a version already indexed by the Registry; publish
a new version instead. Use the Registry's resync action when a completed release
is not discovered, and check its webhook deliveries.
