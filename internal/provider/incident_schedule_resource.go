package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/incident-io/terraform-provider-incident/internal/apischema"
	"github.com/incident-io/terraform-provider-incident/internal/client"
	"github.com/samber/lo"
)

var (
	_ resource.Resource                = &IncidentScheduleResource{}
	_ resource.ResourceWithImportState = &IncidentScheduleResource{}
)

type IncidentScheduleResource struct {
	client           *client.ClientWithResponses
	terraformVersion string
}

type IncidentScheduleResourceModel struct {
	ID        types.String `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	Timezone  types.String `tfsdk:"timezone"`
	Rotations []Rotation   `tfsdk:"rotations"`
}

type Rotation struct {
	ID       types.String      `tfsdk:"id"`
	Name     types.String      `tfsdk:"name"`
	Versions []RotationVersion `tfsdk:"versions"`
}

type RotationVersion struct {
	EffectiveFrom    types.String      `tfsdk:"effective_from"`
	HandoverStartAt  types.String      `tfsdk:"handover_start_at"`
	Handovers        []Handover        `tfsdk:"handovers"`
	Users            []types.String    `tfsdk:"users"`
	WorkingIntervals []WorkingInterval `tfsdk:"working_intervals"`
	Layers           []Layer           `tfsdk:"layers"`
}

type WorkingInterval struct {
	Start types.String `tfsdk:"start"`
	End   types.String `tfsdk:"end"`
	Day   types.String `tfsdk:"day"`
}

type Layer struct {
	ID   types.String `tfsdk:"id"`
	Name types.String `tfsdk:"name"`
}

type Handover struct {
	Interval     types.Int64  `tfsdk:"interval"`
	IntervalType types.String `tfsdk:"interval_type"`
}

func NewIncidentScheduleResource() resource.Resource {
	return &IncidentScheduleResource{}
}

func (r *IncidentScheduleResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_schedule"
}

func (r *IncidentScheduleResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: apischema.TagDocstring("Schedules V2"),
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
				MarkdownDescription: apischema.Docstring("ScheduleV2ResponseBody", "id"),
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: apischema.Docstring("ScheduleV2ResponseBody", "name"),
			},
			"timezone": schema.StringAttribute{
				Required: true,
			},
			"rotations": schema.ListNestedAttribute{
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Required:            true,
							MarkdownDescription: apischema.Docstring("ScheduleRotationV2ResponseBody", "id"),
						},
						"name": schema.StringAttribute{
							Required:            true,
							MarkdownDescription: apischema.Docstring("ScheduleRotationV2ResponseBody", "name"),
						},
						"versions": schema.ListNestedAttribute{
							Required: true,
							NestedObject: schema.NestedAttributeObject{
								Attributes: map[string]schema.Attribute{
									"users": schema.ListAttribute{
										Required:            true,
										ElementType:         types.StringType,
										MarkdownDescription: apischema.Docstring("UserReferencePayloadV1RequestBody", "id"),
									},
									"effective_from": schema.StringAttribute{
										Optional:            true,
										MarkdownDescription: apischema.Docstring("ScheduleRotationV2ResponseBody", "effective_from"),
									},
									"handover_start_at": schema.StringAttribute{
										Required:            true,
										MarkdownDescription: apischema.Docstring("ScheduleRotationV2ResponseBody", "handover_start_at"),
									},
									"working_intervals": schema.ListNestedAttribute{
										Optional:            true,
										MarkdownDescription: apischema.Docstring("ScheduleRotationV2ResponseBody", "working_interval"),
										NestedObject: schema.NestedAttributeObject{
											Attributes: map[string]schema.Attribute{
												"start": schema.StringAttribute{
													Required: true,
												},
												"end": schema.StringAttribute{
													Required: true,
												},
												"day": schema.StringAttribute{
													Required: true,
												},
											},
										},
									},
									"layers": schema.ListNestedAttribute{
										Required:            true,
										MarkdownDescription: apischema.Docstring("ScheduleRotationV2ResponseBody", "layers"),
										NestedObject: schema.NestedAttributeObject{
											Attributes: map[string]schema.Attribute{
												"id": schema.StringAttribute{
													Required: true,
												},
												"name": schema.StringAttribute{
													Required: true,
												},
											},
										},
									},
									"handovers": schema.ListNestedAttribute{
										Optional:            true,
										MarkdownDescription: apischema.Docstring("ScheduleRotationV2ResponseBody", "handovers"),
										NestedObject: schema.NestedAttributeObject{
											Attributes: map[string]schema.Attribute{
												"interval": schema.Int64Attribute{
													Required: true,
												},
												"interval_type": schema.StringAttribute{
													Required: true,
												},
											},
										},
									},
								},
							},
						},
					},
				},
				Required: true,
			},
		},
	}
}

func (r *IncidentScheduleResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*IncidentProviderData)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *client.Client, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)

		return
	}

	r.client = client.Client
	r.terraformVersion = client.TerraformVersion
}

func (r *IncidentScheduleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data *IncidentScheduleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	rotationArray, err := buildScheduleCreatePayload(data, resp)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create schedule, got error: %s", err))
		return
	}

	result, err := r.client.SchedulesV2CreateWithResponse(ctx, client.SchedulesV2CreateJSONRequestBody{
		Schedule: client.ScheduleCreatePayloadV2{
			Annotations: &map[string]string{
				"incident.io/terraform/version": r.terraformVersion,
			},
			Name:     data.Name.ValueStringPointer(),
			Timezone: data.Timezone.ValueStringPointer(),
			Config: &client.ScheduleConfigCreatePayloadV2{
				Rotations: &rotationArray,
			},
		},
	})
	if err == nil && result.StatusCode() >= 400 {
		err = fmt.Errorf(string(result.Body))
	}
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create schedule, got error: %s", err))
		return
	}

	tflog.Trace(ctx, fmt.Sprintf("created an incident schedule resource with id=%s", result.JSON201.Schedule.Id))
	data = r.buildModel(result.JSON201.Schedule)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *IncidentScheduleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data *IncidentScheduleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	result, err := r.client.SchedulesV2ShowWithResponse(ctx, data.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read schedule, got error: %s", err))
		return
	}

	if result.StatusCode() == 404 {
		resp.Diagnostics.AddWarning("Not Found", fmt.Sprintf("Unable to read schedule, got status code: %d", result.StatusCode()))
		resp.State.RemoveResource(ctx)
		return
	}

	data = r.buildModel(result.JSON200.Schedule)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *IncidentScheduleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var old *IncidentScheduleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &old)...)
	if resp.Diagnostics.HasError() {
		return
	}

	rotationArray, err := buildScheduleUpdatePayload(old, resp)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update schedule, got error: %s", err))
		return
	}

	result, err := r.client.SchedulesV2UpdateWithResponse(ctx, old.ID.ValueString(), client.SchedulesV2UpdateJSONRequestBody{
		Schedule: client.ScheduleUpdatePayloadV2{
			Annotations: &map[string]string{
				"incident.io/terraform/version": r.terraformVersion,
			},
			Name:     old.Name.ValueStringPointer(),
			Timezone: old.Timezone.ValueStringPointer(),
			Config: &client.ScheduleConfigUpdatePayloadV2{
				Rotations: &rotationArray,
			},
		},
	})
	if err == nil && result.StatusCode() >= 400 {
		err = fmt.Errorf(string(result.Body))
	}
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update schedule, got error: %s", err))
		return
	}

	old = r.buildModel(result.JSON200.Schedule)
	resp.Diagnostics.Append(resp.State.Set(ctx, &old)...)
}

func (r *IncidentScheduleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data *IncidentScheduleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.SchedulesV2DestroyWithResponse(ctx, data.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete schedule, got error: %s", err))
		return
	}
}

func (r *IncidentScheduleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	claimResource(ctx, r.client, req, resp, client.ManagedResourceV2ResourceTypeSchedule, r.terraformVersion)
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func buildScheduleCreatePayload(data *IncidentScheduleResourceModel, resp *resource.CreateResponse) ([]client.ScheduleRotationCreatePayloadV2, error) {
	rotationArray := make([]client.ScheduleRotationCreatePayloadV2, 0, len(data.Rotations))
	for _, rotation := range data.Rotations {
		for _, version := range rotation.Versions {
			workingIntervals := make([]client.ScheduleRotationWorkingIntervalV2, 0, len(version.WorkingIntervals))
			for _, workingInterval := range version.WorkingIntervals {
				workingIntervalWeekday := client.ScheduleRotationWorkingIntervalV2Weekday(workingInterval.Day.ValueString())
				workingIntervals = append(workingIntervals, client.ScheduleRotationWorkingIntervalV2{
					StartTime: workingInterval.Start.ValueString(),
					EndTime:   workingInterval.End.ValueString(),
					Weekday:   workingIntervalWeekday,
				})
			}

			layers := make([]client.ScheduleLayerCreatePayloadV2, 0, len(version.Layers))
			for _, layer := range version.Layers {
				layers = append(layers, client.ScheduleLayerCreatePayloadV2{
					Id:   layer.ID.ValueStringPointer(),
					Name: layer.Name.ValueString(),
				})
			}

			handoverStartAt, err := time.Parse(time.RFC3339, version.HandoverStartAt.ValueString())
			if err != nil {
				resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create schedule, handover start in invalid format: %s", err))
				return nil, err
			}

			effectiveFrom := buildEffectiveFrom(resp.Diagnostics, version.EffectiveFrom)
			handovers := buildHandoversArray(version.Handovers)
			users := buildUsersArray(version.Users)

			rotationArray = append(rotationArray, client.ScheduleRotationCreatePayloadV2{
				Id:              rotation.ID.ValueStringPointer(),
				Name:            rotation.Name.ValueString(),
				HandoverStartAt: &handoverStartAt,
				EffectiveFrom:   effectiveFrom,
				Handovers:       &handovers,
				Users:           &users,
				WorkingInterval: &workingIntervals,
				Layers:          &layers,
			})
		}
	}
	return rotationArray, nil
}

func buildScheduleUpdatePayload(data *IncidentScheduleResourceModel, resp *resource.UpdateResponse) ([]client.ScheduleRotationUpdatePayloadV2, error) {
	rotationArray := make([]client.ScheduleRotationUpdatePayloadV2, 0, len(data.Rotations))
	for _, rotation := range data.Rotations {
		for _, version := range rotation.Versions {
			workingIntervals := make([]client.ScheduleRotationWorkingIntervalUpdatePayloadV2, 0, len(version.WorkingIntervals))
			for _, workingInterval := range version.WorkingIntervals {
				workingIntervalWeekday := client.ScheduleRotationWorkingIntervalUpdatePayloadV2Weekday(workingInterval.Day.ValueString())
				workingIntervals = append(workingIntervals, client.ScheduleRotationWorkingIntervalUpdatePayloadV2{
					StartTime: workingInterval.Start.ValueStringPointer(),
					EndTime:   workingInterval.End.ValueStringPointer(),
					Weekday:   &workingIntervalWeekday,
				})
			}

			layers := make([]client.ScheduleLayerV2, 0, len(version.Layers))
			for _, layer := range version.Layers {
				layers = append(layers, client.ScheduleLayerV2{
					Id:   layer.ID.ValueStringPointer(),
					Name: layer.Name.ValueStringPointer(),
				})
			}

			handoverStartAt, err := time.Parse(time.RFC3339, version.HandoverStartAt.ValueString())
			if err != nil {
				resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create schedule, handover start in invalid format: %s", err))
				return nil, err
			}

			effectiveFrom := buildEffectiveFrom(resp.Diagnostics, version.EffectiveFrom)
			handovers := buildHandoversArray(version.Handovers)
			users := buildUsersArray(version.Users)

			rotationArray = append(rotationArray, client.ScheduleRotationUpdatePayloadV2{
				Id:              rotation.ID.ValueStringPointer(),
				Name:            rotation.Name.ValueStringPointer(),
				HandoverStartAt: &handoverStartAt,
				EffectiveFrom:   effectiveFrom,
				Handovers:       &handovers,
				Users:           &users,
				WorkingInterval: &workingIntervals,
				Layers:          &layers,
			})
		}
	}
	return rotationArray, nil
}

// buildUsersArray converts a list of user IDs to a list of user references.
func buildUsersArray(users []types.String) []client.UserReferencePayloadV1 {
	return lo.Map(users, func(user types.String, _ int) client.UserReferencePayloadV1 {
		return client.UserReferencePayloadV1{
			Id: user.ValueStringPointer(),
		}
	})
}

// buildHandoversArray converts a list of handovers to a list of handover references.
func buildHandoversArray(handovers []Handover) []client.ScheduleRotationHandoverV2 {
	clientHandovers := lo.Map(handovers, func(handover Handover, _ int) client.ScheduleRotationHandoverV2 {
		intervalType := client.ScheduleRotationHandoverV2IntervalType(handover.IntervalType.ValueString())
		return client.ScheduleRotationHandoverV2{
			Interval:     handover.Interval.ValueInt64Pointer(),
			IntervalType: &intervalType,
		}
	})
	return clientHandovers
}

// buildEffectiveFrom converts a string to a time.Time pointer.
func buildEffectiveFrom(diagnostics diag.Diagnostics, effectiveFrom types.String) *time.Time {
	if effectiveFrom.IsNull() {
		return nil
	}

	effectiveFromParsed, err := time.Parse(time.RFC3339, effectiveFrom.ValueString())
	if err != nil {
		diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create schedule, effective from in invalid format: %s", err))
		return nil
	}

	return &effectiveFromParsed
}

// buildModel converts a schedule from the API to a resource model
// this involves taking schedule rotations, grouping them by ID,
// extracting the shared data, and then building the nested structure.
func (r *IncidentScheduleResource) buildModel(schedule client.ScheduleV2) *IncidentScheduleResourceModel {
	rotationsGroupedByID := lo.GroupBy(schedule.Config.Rotations, func(rotation client.ScheduleRotationV2) string {
		return rotation.Id
	})

	type RotationName struct {
		ID   string
		Name string
	}

	rotationNames := lo.Map(schedule.Config.Rotations, func(rotation client.ScheduleRotationV2, _ int) RotationName {
		return RotationName{
			ID:   rotation.Id,
			Name: rotation.Name,
		}
	})

	rotationNames = lo.Uniq(rotationNames)

	return &IncidentScheduleResourceModel{
		Name:     types.StringValue(schedule.Name),
		ID:       types.StringValue(schedule.Id),
		Timezone: types.StringValue(schedule.Timezone),
		Rotations: lo.Map(rotationNames, func(rotation RotationName, _ int) Rotation {
			newRotation := Rotation{
				ID:   types.StringValue(rotation.ID),
				Name: types.StringValue(rotation.Name),
				Versions: lo.Map(rotationsGroupedByID[rotation.ID], func(rotation client.ScheduleRotationV2, idx int) RotationVersion {
					var workingIntervals []WorkingInterval
					if rotation.WorkingInterval != nil {
						workingIntervals = lo.Map(*rotation.WorkingInterval, func(interval client.ScheduleRotationWorkingIntervalV2, _ int) WorkingInterval {
							weekdayString := string(interval.Weekday)
							return WorkingInterval{
								Start: types.StringValue(interval.StartTime),
								End:   types.StringValue(interval.EndTime),
								Day:   types.StringValue(weekdayString),
							}
						})
					}

					layers := lo.Map(rotation.Layers, func(layer client.ScheduleLayerV2, _ int) Layer {
						return Layer{
							ID:   types.StringPointerValue(layer.Id),
							Name: types.StringPointerValue(layer.Name),
						}
					})

					handovers := lo.Map(rotation.Handovers, func(handover client.ScheduleRotationHandoverV2, _ int) Handover {
						intervalTypeString := string(*handover.IntervalType)
						return Handover{
							Interval:     types.Int64Value(lo.FromPtr(handover.Interval)),
							IntervalType: types.StringValue(intervalTypeString),
						}
					})

					users := []types.String{}
					if rotation.Users != nil {
						users = lo.Map(lo.FromPtr(rotation.Users), func(user client.UserV1, _ int) types.String {
							return types.StringValue(user.Id)
						})
					}

					var effectiveFrom types.String
					if rotation.EffectiveFrom != nil {
						effectiveFromValue := rotation.EffectiveFrom.Format(time.RFC3339)
						effectiveFrom = types.StringValue(effectiveFromValue)
					} else {
						effectiveFrom = types.StringNull()
					}

					handoverStartAt := types.StringValue(rotation.HandoverStartAt.Format(time.RFC3339))

					return RotationVersion{
						EffectiveFrom:    effectiveFrom,
						Handovers:        handovers,
						Users:            users,
						WorkingIntervals: workingIntervals,
						Layers:           layers,
						HandoverStartAt:  handoverStartAt,
					}
				}),
			}
			return newRotation
		}),
	}
}
