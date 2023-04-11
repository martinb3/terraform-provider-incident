# Load a catalog from a JSON file. You may also use an HTTP endpoint or some
# other data source, or prepare this file using a script before terraform runs.
#
# We'll use an example taken from Backstage:
# https://backstage.io/docs/features/software-catalog/descriptor-format
#
/*
{
  "apiVersion": "backstage.io/v1alpha1",
  "kind": "Component",
  "metadata": {
    "annotations": {
      "backstage.io/managed-by-location": "file:/tmp/catalog-info.yaml",
      "example.com/service-discovery": "artistweb",
      "circleci.com/project-slug": "github/example-org/artist-website"
    },
    "description": "The place to be, for great artists",
    "etag": "ZjU2MWRkZWUtMmMxZS00YTZiLWFmMWMtOTE1NGNiZDdlYzNk",
    "labels": {
      "example.com/custom": "custom_label_value"
    },
    "links": [
      {
        "url": "https://admin.example-org.com",
        "title": "Admin Dashboard",
        "icon": "dashboard",
        "type": "admin-dashboard"
      }
    ],
    "tags": [
      "java"
    ],
    "name": "artist-web",
    "uid": "2152f463-549d-4d8d-a94d-ce2b7676c6e2"
  },
  "spec": {
    "lifecycle": "production",
    "owner": "artist-relations-team",
    "type": "website",
    "system": "public-websites"
  }
}
*/
locals {
  catalog = {
    for entry in jsondecode(file("catalog.json")) : entry["uid"] => entry
  }
}

################################################################################
# Create the type
################################################################################

# Define the catalog type, creating attributes that map to the values in the
# catalog data source.
resource "incident_catalog_type" "service" {
  name        = "Service"
  description = "All services that we run at Example Org"
}

resource "incident_catalog_type_attribute" "service_owner" {
  catalog_type_id = incident_catalog_type.service.id

  name = "Owner"
  type = "String"
}

resource "incident_catalog_type_attribute" "service_description" {
  catalog_type_id = incident_catalog_type.service.id

  name = "Description"
  type = "String"
}

resource "incident_catalog_type_attribute" "service_tags" {
  catalog_type_id = incident_catalog_type.service.id

  name  = "Tags"
  type  = "String"
  array = true
}

################################################################################
# Provision the entries
################################################################################

# This is where we create all the entries. If we have any entries in Service
# that are not defined in this resource, we will delete them.
resource "incident_catalog_entries" "services" {
  id = incident_catalog_type.service.id

  entries = {
    # Map from the catalog external ID => entry value.
    for external_id, entry in local.catalog :

    # e.g. 2152f463-549d-4d8d-a94d-ce2b7676c6e2
    external_id => {
      # e.g. artist-web
      name = entry["metadata"]["name"],

      # In this catalog we know names are unique, so we can use them as a
      # human-friendly unique alias. Other catalogs name may not be unique, in
      # which case this would fail.
      alias = entry["metadata"]["name"],

      # Now build all attribute values for this entry, with an object
      # comprehension that filters out any attributes that we are missing values
      # for.
      attribute_values = {
        for attribute, binding in {
          # Owner (e.g. artist-relations-team)
          (incident_catalog_type_attribute.service_owner.id) = {
            value = try(entry["spec"]["owner"], null)
          },

          # Description (e.g. The place to be, for great artists)
          (incident_catalog_type_attribute.service_description.id) = {
            value = try(entry["metadata"]["description"], null)
          },

          # Tags (e.g. ["java"])
          (incident_catalog_type_attribute.service_tags.id) = {
            array_value = try(entry["metadata"]["tags"], null)
          },
        } : attribute => binding if try(binding.value, binding.array_value) != null
      }
    }
  }
}
