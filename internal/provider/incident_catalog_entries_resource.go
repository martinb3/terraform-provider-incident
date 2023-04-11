package provider

import (
	"context"
	"fmt"
	"reflect"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/incident-io/terraform-provider-incident/internal/apischema"
	"github.com/incident-io/terraform-provider-incident/internal/client"
	"github.com/pkg/errors"
	"github.com/samber/lo"
	"golang.org/x/sync/errgroup"
)

var (
	_ resource.Resource                = &IncidentCatalogEntriesResource{}
	_ resource.ResourceWithImportState = &IncidentCatalogEntriesResource{}
)

type IncidentCatalogEntriesResource struct {
	client *client.ClientWithResponses
}

type IncidentCatalogEntriesResourceModel struct {
	ID      types.String                 `tfsdk:"id"` // Catalog Type ID
	Entries map[string]CatalogEntryModel `tfsdk:"entries"`
}

type CatalogEntryModel struct {
	ID              types.String                                 `tfsdk:"id"`
	Name            types.String                                 `tfsdk:"name"`
	Alias           types.String                                 `tfsdk:"alias"`
	Rank            types.Int64                                  `tfsdk:"rank"`
	AttributeValues map[string]CatalogEntryAttributeBindingModel `tfsdk:"attribute_values"`

	externalID string // tracks the external ID for our internal book-keeping
}

type CatalogEntryAttributeBindingModel struct {
	Value      types.String `tfsdk:"value"`
	ArrayValue types.List   `tfsdk:"array_value"`
}

func NewIncidentCatalogEntriesResource() resource.Resource {
	return &IncidentCatalogEntriesResource{}
}

func (r *IncidentCatalogEntriesResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_catalog_entries"
}

func (r *IncidentCatalogEntriesResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `
This resource manages all entries for a given catalog type and should be used when
loading many (>100) catalog entries to ensure fast and reliable plans.

Please note that this resource is authoritative, in that it will delete all entries from
the catalog type that it doesn't manage, even those created outside of terraform.

If you have a catalog source such as Backstage or some custom catalog you'd like to sync
into incident.io, this is the recommended way of achieving that.

## External IDs

As this resource loads content from an existing catalog source into the incident.io
catalog, it requires that each entry is given a stable identifier that can uniquely
identify it in the upstream system.

We call this the 'external ID' and might be something like:

- The ID of the entry in a custom catalog, often the primary key of the entry
- Any stable human identifier (often called a slug) that uniquely reference the entry

This external ID is what we use as a map key for the entries attribute, and how we map
changes to one entry to an update to that same entry when the upstream changes.
		`,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: apischema.Docstring("CatalogEntryV2ResponseBody", "catalog_type_id"),
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Required: true,
			},
			"entries": schema.MapNestedAttribute{
				Required:            true,
				MarkdownDescription: `Map of external ID to entry in the catalog.`,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							MarkdownDescription: apischema.Docstring("CatalogEntryV2ResponseBody", "id"),
							Computed:            true,
							PlanModifiers: []planmodifier.String{
								stringplanmodifier.UseStateForUnknown(),
							},
						},
						"name": schema.StringAttribute{
							MarkdownDescription: apischema.Docstring("CatalogEntryV2ResponseBody", "name"),
							Required:            true,
						},
						"alias": schema.StringAttribute{
							MarkdownDescription: apischema.Docstring("CatalogEntryV2ResponseBody", "alias"),
							Optional:            true,
						},
						"rank": schema.Int64Attribute{
							MarkdownDescription: apischema.Docstring("CatalogEntryV2ResponseBody", "rank"),
							Optional:            true,
							Computed:            true,
							Default:             int64default.StaticInt64(0),
						},
						"attribute_values": schema.MapNestedAttribute{
							Required: true,
							NestedObject: schema.NestedAttributeObject{
								Attributes: map[string]schema.Attribute{
									"value": schema.StringAttribute{
										Description: `The value of this attribute, in a format suitable for this attribute type.`,
										Optional:    true,
									},
									"array_value": schema.ListAttribute{
										ElementType: types.StringType,
										Description: `The value of this element of the array, in a format suitable for this attribute type.`,
										Optional:    true,
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func (r *IncidentCatalogEntriesResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*client.ClientWithResponses)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *client.Client, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)

		return
	}

	r.client = client
}

func (r *IncidentCatalogEntriesResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data *IncidentCatalogEntriesResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	catalogType, entries, err := r.reconcile(ctx, data)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", err.Error())
		return
	}

	data = r.buildModel(*catalogType, entries)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *IncidentCatalogEntriesResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data *IncidentCatalogEntriesResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	catalogType, entries, err := r.getEntries(ctx, data.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list entries, got error: %s", err))
		return
	}

	data = r.buildModel(*catalogType, entries)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *IncidentCatalogEntriesResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data *IncidentCatalogEntriesResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	catalogType, entries, err := r.reconcile(ctx, data)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", err.Error())
		return
	}

	data = r.buildModel(*catalogType, entries)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *IncidentCatalogEntriesResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data *IncidentCatalogEntriesResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Set entries to an empty list.
	data.Entries = map[string]CatalogEntryModel{}

	catalogType, entries, err := r.reconcile(ctx, data)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", err.Error())
		return
	}
	if len(entries) > 0 {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("tried deleting all entries but found %d for catalog type id=%s", len(entries), catalogType.Id))
		return
	}
}

func (r *IncidentCatalogEntriesResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// buildModel generates a terraform model from a catalog type and current list of all
// entries, as received from getEntries.
func (r *IncidentCatalogEntriesResource) buildModel(catalogType client.CatalogTypeV2, entries []client.CatalogEntryV2) *IncidentCatalogEntriesResourceModel {
	modelEntries := map[string]CatalogEntryModel{}
	for _, entry := range entries {
		// Skip all entries that come with no external ID, as these can't have been created by
		// terraform, and therefore should never be managed by us.
		if entry.ExternalId == nil {
			continue
		}

		values := map[string]CatalogEntryAttributeBindingModel{}
		for attributeID, binding := range entry.AttributeValues {
			value := CatalogEntryAttributeBindingModel{
				ArrayValue: types.ListNull(types.StringType),
			}
			// The API can behave weirdly in the case of empty arrays and omit the field entirely.
			// This is painful for us as terraform will see the omission as a diff against the
			// state, so we paper over the issue by instantiating an empty array value if we think
			// we're seeing the weirdness.
			if binding.Value == nil && binding.ArrayValue == nil {
				binding.ArrayValue = lo.ToPtr([]client.CatalogAttributeValueV2{})
			}

			if binding.Value != nil {
				value.Value = types.StringValue(*binding.Value.Literal)
			}
			if binding.ArrayValue != nil {
				elements := []attr.Value{}
				for _, value := range *binding.ArrayValue {
					elements = append(elements, types.StringValue(*value.Literal))
				}

				value.ArrayValue = types.ListValueMust(types.StringType, elements)
			}

			values[attributeID] = value
		}

		alias := types.StringNull()
		if entry.Alias != nil {
			alias = types.StringValue(*entry.Alias)
		}

		modelEntries[*entry.ExternalId] = CatalogEntryModel{
			ID:              types.StringValue(entry.Id),
			Name:            types.StringValue(entry.Name),
			Alias:           alias,
			Rank:            types.Int64Value(int64(entry.Rank)),
			AttributeValues: values,
			externalID:      *entry.ExternalId,
		}
	}

	return &IncidentCatalogEntriesResourceModel{
		ID:      types.StringValue(catalogType.Id),
		Entries: modelEntries,
	}
}

type catalogEntryModelPayload struct {
	CatalogEntryID *string
	Payload        client.CreateEntryRequestBody
}

// buildPayloads produces a list of payloads that are used to either create or update an
// entry depending on whether we're already tracking it in our model.
func (m IncidentCatalogEntriesResourceModel) buildPayloads() []*catalogEntryModelPayload {
	payloads := []*catalogEntryModelPayload{}
	for externalID, entry := range m.Entries {
		values := map[string]client.CatalogAttributeBindingPayloadV2{}
		for attributeID, attributeValue := range entry.AttributeValues {
			payload := client.CatalogAttributeBindingPayloadV2{}
			if !attributeValue.Value.IsNull() {
				payload.Value = &client.CatalogAttributeValuePayloadV2{
					Literal: lo.ToPtr(attributeValue.Value.ValueString()),
				}
			}
			if !attributeValue.ArrayValue.IsNull() {
				arrayValue := []client.CatalogAttributeValuePayloadV2{}
				for _, element := range attributeValue.ArrayValue.Elements() {
					elementString, ok := element.(types.String)
					if !ok {
						panic(fmt.Sprintf("element should have been types.String but was %T", element))
					}
					arrayValue = append(arrayValue, client.CatalogAttributeValuePayloadV2{
						Literal: lo.ToPtr(elementString.ValueString()),
					})
				}

				payload.ArrayValue = &arrayValue
			}

			values[attributeID] = payload
		}

		payload := &catalogEntryModelPayload{
			Payload: client.CreateEntryRequestBody{
				CatalogTypeId:   m.ID.ValueString(),
				Name:            entry.Name.ValueString(),
				ExternalId:      lo.ToPtr(externalID),
				AttributeValues: values,
				Rank:            nil,
			},
		}
		if !entry.ID.IsUnknown() {
			payload.CatalogEntryID = lo.ToPtr(entry.ID.ValueString())
		}
		if !entry.Alias.IsUnknown() && !entry.Alias.IsNull() {
			payload.Payload.Alias = lo.ToPtr(entry.Alias.ValueString())
		}
		if !entry.Rank.IsUnknown() && !entry.Rank.IsNull() {
			payload.Payload.Rank = lo.ToPtr(int32(entry.Rank.ValueInt64()))
		}

		payloads = append(payloads, payload)
	}

	return payloads
}

func (r *IncidentCatalogEntriesResource) getEntries(ctx context.Context, catalogTypeID string) (catalogType *client.CatalogTypeV2, entries []client.CatalogEntryV2, err error) {
	var (
		after *string
	)

	for {
		result, err := r.client.CatalogV2ListEntriesWithResponse(ctx, &client.CatalogV2ListEntriesParams{
			CatalogTypeId: catalogTypeID,
			PageSize:      lo.ToPtr(int64(250)),
			After:         after,
		})
		if err == nil && result.StatusCode() >= 400 {
			err = fmt.Errorf(string(result.Body))
		}
		if err != nil {
			return nil, nil, errors.Wrap(err, "listing entries")
		}

		entries = append(entries, result.JSON200.CatalogEntries...)
		if count := len(result.JSON200.CatalogEntries); count == 0 {
			return &result.JSON200.CatalogType, entries, nil // end pagination
		} else {
			after = lo.ToPtr(result.JSON200.CatalogEntries[count-1].Id)
		}
	}
}

// reconcile is a bit of a hack, in that terraform resources don't often work like this,
// but is the best way to achieve our goals a resource which manages a fair amount of
// data.
//
// It works by taking a terraform model representing the combination of terraform code and
// existing state for all entries, then loading all the current entries and matching model
// against real world.
//
// For any of the entries that don't match the model, either because they don't exist or
// because something has been changed, we will schedule for deletion. But we begin by
// deleting all entries for which we don't have a match in our model, essentially cleaning
// house before starting over fresh.
//
// This is how we create, update and destroy this terraform resource.
func (r *IncidentCatalogEntriesResource) reconcile(ctx context.Context, data *IncidentCatalogEntriesResourceModel) (*client.CatalogTypeV2, []client.CatalogEntryV2, error) {
	_, entries, err := r.getEntries(ctx, data.ID.ValueString())
	if err != nil {
		return nil, nil, errors.Wrap(err, "listing entries")
	}

	{
		toDelete := []client.CatalogEntryV2{}
	eachEntry:
		for _, entry := range entries {
			if entry.ExternalId != nil {
				_, ok := data.Entries[*entry.ExternalId]
				if ok {
					continue eachEntry // we know the ID and we've found a match, so skip
				}
			}

			// We can't find this entry in our model, or it never had an external ID, which
			// means we want to delete it.
			toDelete = append(toDelete, entry)
		}

		tflog.Debug(ctx, fmt.Sprintf("found %d entries in the catalog, want to delete %d of them", len(entries), len(toDelete)))

		g, ctx := errgroup.WithContext(ctx)
		g.SetLimit(10)

		for _, entry := range toDelete {
			var (
				entry = entry // avoid shadow loop variable
			)
			g.Go(func() error {
				result, err := r.client.CatalogV2DestroyEntryWithResponse(ctx, entry.Id)
				if err == nil && result.StatusCode() >= 400 {
					err = fmt.Errorf(string(result.Body))
				}
				if err != nil {
					return errors.Wrap(err, "unable to destroy catalog entry, got error")
				}

				tflog.Debug(ctx, fmt.Sprintf("destroyed catalog entry with id=%s", entry.Id))

				return nil
			})
		}

		if err := g.Wait(); err != nil {
			return nil, nil, errors.Wrap(err, "destroying catalog entries")
		}
	}

	// We only care about entries with an external ID, as we should have deleted all that
	// didn't have one above. We also want this lookup to be fast to help when the entry
	// list is very long.
	entriesByExternalID := map[string]*client.CatalogEntryV2{}
	for _, entry := range entries {
		if entry.ExternalId == nil {
			continue
		}

		entriesByExternalID[*entry.ExternalId] = lo.ToPtr(entry)
	}

	{
		g, ctx := errgroup.WithContext(ctx)
		g.SetLimit(10)

		// For everything in our model, we know we either want to create or update it.
	eachPayload:
		for _, payload := range data.buildPayloads() {
			var (
				payload      = payload              // alias this for concurrent loop
				shouldUpdate bool                   // mark this if we think we should update things
				entry        *client.CatalogEntryV2 // existing entry
			)

			entry, alreadyExists := entriesByExternalID[*payload.Payload.ExternalId]
			if alreadyExists {
				// If we found the entry in the list of all entries, then we need to diff it and
				// update as appropriate.
				if entry != nil {
					isSame :=
						reflect.DeepEqual(payload.Payload.Name, entry.Name) &&
							reflect.DeepEqual(payload.Payload.Alias, entry.Alias) &&
							(payload.Payload.Rank == nil || (*payload.Payload.Rank == entry.Rank))

					currentBindings := map[string]client.CatalogAttributeBindingPayloadV2{}
					for attributeID, value := range entry.AttributeValues {
						current := client.CatalogAttributeBindingPayloadV2{}
						if value.ArrayValue != nil {
							current.ArrayValue = lo.ToPtr(lo.Map(*value.ArrayValue, func(binding client.CatalogAttributeValueV2, _ int) client.CatalogAttributeValuePayloadV2 {
								return client.CatalogAttributeValuePayloadV2{
									Literal: binding.Literal,
								}
							}))
						}
						if value.Value != nil {
							current.Value = &client.CatalogAttributeValuePayloadV2{
								Literal: value.Value.Literal,
							}
						}

						currentBindings[attributeID] = current
					}

					if isSame && reflect.DeepEqual(payload.Payload.AttributeValues, currentBindings) {
						tflog.Debug(ctx, fmt.Sprintf("catalog entry with id=%s has not changed, not updating", entry.Id))
						continue eachPayload
					} else {
						tflog.Debug(ctx, fmt.Sprintf("catalog entry with id=%s has changed, scheduling for update", entry.Id))
						shouldUpdate = true
					}
				}
			}

			g.Go(func() error {
				if shouldUpdate {
					result, err := r.client.CatalogV2UpdateEntryWithResponse(ctx, entry.Id, client.UpdateEntryRequestBody{
						Name:            payload.Payload.Name,
						ExternalId:      payload.Payload.ExternalId,
						Rank:            payload.Payload.Rank,
						Alias:           payload.Payload.Alias,
						AttributeValues: payload.Payload.AttributeValues,
					})
					if err == nil && result.StatusCode() >= 400 {
						err = fmt.Errorf(string(result.Body))
					}
					if err != nil {
						return errors.Wrap(err, fmt.Sprintf("unable to update catalog entry with id=%s, got error", *payload.CatalogEntryID))
					}

					tflog.Debug(ctx, fmt.Sprintf("updated catalog entry with id=%s", entry.Id))
				} else {
					result, err := r.client.CatalogV2CreateEntryWithResponse(ctx, client.CreateEntryRequestBody{
						CatalogTypeId:   data.ID.ValueString(),
						Name:            payload.Payload.Name,
						ExternalId:      payload.Payload.ExternalId,
						Rank:            payload.Payload.Rank,
						Alias:           payload.Payload.Alias,
						AttributeValues: payload.Payload.AttributeValues,
					})
					if err == nil && result.StatusCode() >= 400 {
						err = fmt.Errorf(string(result.Body))
					}
					if err != nil {
						return errors.Wrap(err, fmt.Sprintf("unable to create catalog entry with external_id=%s, got error", *payload.Payload.ExternalId))
					}

					tflog.Debug(ctx, fmt.Sprintf("created a catalog entry resource with id=%s", result.JSON201.CatalogEntry.Id))
				}

				return nil
			})
		}

		if err := g.Wait(); err != nil {
			return nil, nil, errors.Wrap(err, "reconciling catalog entries")
		}
	}

	catalogType, entries, err := r.getEntries(ctx, data.ID.ValueString())
	if err != nil {
		return nil, nil, errors.Wrap(err, "listing entries")
	}

	return catalogType, entries, nil
}