---
page_title: "Migrating from namecheap/jenkins"
subcategory: ""
description: |-
  Move existing Terraform configuration and state from namecheap/jenkins to pubg/jenkins.
---

# Migrating from namecheap/jenkins

`pubg/jenkins` is maintained from the upstream `namecheap/jenkins` codebase.
Resource names keep the `jenkins_` prefix. The first PUBG release is based on
upstream 1.2.8 and adds managed Config File Provider files.

After the PUBG release is available in the Terraform Registry, update every
module's provider requirement:

```hcl
terraform {
  required_providers {
    jenkins = {
      source  = "pubg/jenkins"
      version = "~> 1.3"
    }
  }
}
```

Back up the current state using your backend's normal process, then update the
provider reference and install the new provider:

```sh
terraform state replace-provider registry.terraform.io/namecheap/jenkins registry.terraform.io/pubg/jenkins
terraform init -upgrade
terraform plan
```

Review the plan before applying. The provider address change alone should not
require resource recreation. An older upstream version may also need the
schema migrations documented in the [1.2.2 upgrade guide](upgrading-to-1.2.2.md).
Commit the updated dependency lock file after reviewing it.

If you use `dev_overrides`, change its key to `pubg/jenkins` too. Remove that
override when verifying installation of an actual Registry release.
