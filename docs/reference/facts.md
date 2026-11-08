# Facts

You learn which resources, modules, and outputs a state snapshot
records. Because state can be stale, read these facts as "what
Terraform last applied," not "what the cloud has right now."

The table below mirrors `spec.emittedFacts` in
[manifest.yaml](../../manifest.yaml); update this table in the same
change as the manifest. That file declares 9 fact kinds, all at
schema version `1.0.0` with `observed` confidence (read directly
from the state artifact).

| Fact kind | What you get |
|---|---|
| `terraform_state_snapshot` | One state snapshot as a whole, with its digest |
| `terraform_state_resource` | One resource recorded in the state |
| `terraform_state_output` | One Terraform output recorded in the state |
| `terraform_state_module` | One module recorded in the state |
| `terraform_state_provider_binding` | Which provider a module or resource is bound to |
| `terraform_state_tag_observation` | Tags seen on one state resource |
| `terraform_state_warning` | Part of the state could not be read or trusted fully |
| `incident_routing.applied_pagerduty_resource` | A PagerDuty resource Terraform applied |
| `incident_routing.applied_alert_route` | An alert route Terraform applied |

New to the words *fact* and *claim*? [Concepts](../concepts/README.md) defines them in plain language.
