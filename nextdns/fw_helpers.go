package nextdns

import (
	"context"
	"fmt"

	"github.com/Aeronet-Wireless-Broadband-LLC/terraform-provider-nextdns/internal/nextdns"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// idAttribute is the id every resource carried implicitly under SDKv2. It
// always equals the profile ID.
func idAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		Description:   "The profile identifier; the same value as `profile_id`.",
		Computed:      true,
		PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
}

// profileIDAttribute is the profile_id every profile-scoped resource takes.
// As under SDKv2 it is not ForceNew: changing it writes the new profile and
// leaves the old one as it was.
func profileIDAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		Description: "The profile identifier to target the resource.",
		Required:    true,
	}
}

// resetOnDestroyAttribute is the opt-in to the destroy behaviour of earlier
// releases, where destroying a section resource reset that section of the
// live profile. reset describes what a reset does to this resource.
func resetOnDestroyAttribute(reset string) schema.BoolAttribute {
	return schema.BoolAttribute{
		Description: "Whether destroying this resource also resets the live profile. Defaults to `false`: destroy only removes the resource from state and leaves the live profile unchanged. " +
			"With `true`, destroy " + reset + ", as releases before the AeroNet fork always did. " +
			"Destroy uses the value recorded in state, so apply a change to this attribute before destroying.",
		Optional: true,
		Computed: true,
		Default:  booldefault.StaticBool(false),
	}
}

// boolOrDefault returns v, or def when v is null or unknown: state written
// before the attribute existed, and state just imported, carry no value for
// it, and Read records the default so the plan stays empty.
func boolOrDefault(v types.Bool, def bool) types.Bool {
	if v.IsNull() || v.IsUnknown() {
		return types.BoolValue(def)
	}
	return v
}

// keepLiveOnDestroy reports whether Delete must leave the live profile alone,
// which is the case unless reset_on_destroy is true in state. It then adds a
// warning saying that only the state entry was removed.
func keepLiveOnDestroy(reset types.Bool, resourceType, profileID string, diags *diag.Diagnostics) bool {
	if reset.ValueBool() {
		return false
	}
	diags.AddWarning(
		"Removed from state only",
		fmt.Sprintf("%s for profile %s was removed from state; the live profile was not changed. "+
			"To have destroy reset it instead, set reset_on_destroy = true and apply before destroying.", resourceType, profileID),
	)
	return true
}

// clientFrom unpacks the API client the provider's Configure produced. It
// returns nil without a diagnostic when the provider is not configured yet,
// which the framework does during validation.
func clientFrom(data any, diags *diag.Diagnostics) *nextdns.Client {
	if data == nil {
		return nil
	}
	client, ok := data.(*nextdns.Client)
	if !ok {
		diags.AddError("Unexpected provider data", fmt.Sprintf("expected *nextdns.Client, got %T", data))
		return nil
	}
	return client
}

// stringIDs returns the elements of a set of strings; null gives nil.
func stringIDs(ctx context.Context, s types.Set, diags *diag.Diagnostics) []string {
	if s.IsNull() || s.IsUnknown() {
		return nil
	}
	var out []string
	diags.Append(s.ElementsAs(ctx, &out, false)...)
	return out
}

// stringListFromAPI turns the ids the API returned into a list value for
// state. An empty result keeps the prior value when that was null or empty.
// The data sources use it; the resources hold sets (stringSetFromAPI).
func stringListFromAPI(ids []string, prior types.List) types.List {
	if len(ids) == 0 && (prior.IsNull() || (!prior.IsUnknown() && len(prior.Elements()) == 0)) {
		return prior
	}
	elems := make([]attr.Value, 0, len(ids))
	for _, id := range ids {
		elems = append(elems, types.StringValue(id))
	}
	return types.ListValueMust(types.StringType, elems)
}

// stringSetFromAPI turns the ids the API returned into a set value for
// state, so the order the API lists them in never shows as a change. An
// empty result keeps the prior value when that was null or empty, so an
// omitted set stays null and an explicit [] stays []: the framework plans a
// change between the two, where SDKv2 treated them as the same.
func stringSetFromAPI(ids []string, prior types.Set) types.Set {
	if len(ids) == 0 && (prior.IsNull() || (!prior.IsUnknown() && len(prior.Elements()) == 0)) {
		return prior
	}
	elems := make([]attr.Value, 0, len(ids))
	for _, id := range ids {
		elems = append(elems, types.StringValue(id))
	}
	return types.SetValueMust(types.StringType, elems)
}
