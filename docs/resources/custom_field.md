---
# generated by https://github.com/hashicorp/terraform-plugin-docs
page_title: "incident_custom_field Resource - terraform-provider-incident"
subcategory: ""
description: |-
  Manage custom fields.
  Custom fields are used to attach metadata to incidents, which you can use when searching
  for incidents in the dashboard, triggering workflows, building announcement rules or for
  your own data needs.
  Each field has a type:
  Single-select, single value selected from a predefined list of options (ie. Incident Type)Multi-select, as above but you can pick more than one option (ie. Teams)Text, freeform text field (ie. Customer ID)Link, link URL that is synced to Slack bookmarks on the incident channel (ie. External Status Page)Number, integer or fractional numbers (ie. Customers Affected)
  We may add more custom field types in the future - we'd love to hear any other types you'd like to use!
---

# incident_custom_field (Resource)

Manage custom fields.

Custom fields are used to attach metadata to incidents, which you can use when searching
for incidents in the dashboard, triggering workflows, building announcement rules or for
your own data needs.

Each field has a type:

- Single-select, single value selected from a predefined list of options (ie. Incident Type)
- Multi-select, as above but you can pick more than one option (ie. Teams)
- Text, freeform text field (ie. Customer ID)
- Link, link URL that is synced to Slack bookmarks on the incident channel (ie. External Status Page)
- Number, integer or fractional numbers (ie. Customers Affected)

We may add more custom field types in the future - we'd love to hear any other types you'd like to use!

## Example Usage

```terraform
# Create an Affected Teams multi-select field, required always, shown at all
# opportunities.
resource "incident_incident_role" "affected_teams" {
  name        = "Affected Teams"
  description = "The teams that are affected by this incident."
  field_type  = "multi_select"
  required    = "always"

  show_before_creation      = true
  show_before_closure       = true
  show_before_update        = true
  show_in_announcement_post = true
}
```

<!-- schema generated by tfplugindocs -->
## Schema

### Required

- `description` (String) Description of the custom field
- `field_type` (String) Type of custom field
- `name` (String) Human readable name for the custom field
- `required` (String) When this custom field must be set during the incident lifecycle.
- `show_before_closure` (Boolean) Whether a custom field should be shown in the incident close modal. If this custom field is required before closure, but no value has been set for it, the field will be shown in the closure modal whatever the value of this setting.
- `show_before_creation` (Boolean) Whether a custom field should be shown in the incident creation modal. This must be true if the field is always required.
- `show_before_update` (Boolean) Whether a custom field should be shown in the incident update modal.
- `show_in_announcement_post` (Boolean) Whether a custom field should be shown in the list of fields as part of the announcement post when set.

### Read-Only

- `id` (String) Unique identifier for the custom field

