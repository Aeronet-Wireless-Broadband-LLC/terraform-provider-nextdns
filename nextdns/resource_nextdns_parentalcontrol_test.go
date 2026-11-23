// editorconfig-checker-disable-file: HCL in raw strings is space-indented.
package nextdns

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

// Regression for the ported cozy-corner fix: the profile-level PATCH must
// not carry services/categories (they have their own endpoints), and a
// recreation block with only a timezone (times: null) must not panic.
func TestParentalControl_PatchOmitsServicesAndCategoriesAndTimesMayBeNull(t *testing.T) {
	f := newFakeAPI(t)
	f.seed("abc123", "parentalControl", `{"safeSearch": false, "youtubeRestrictedMode": false, "blockBypass": true, "recreation": {"times": null, "timezone": ""}}`)
	f.seed("abc123", "parentalControl/services", `[]`)
	f.seed("abc123", "parentalControl/categories", `[]`)

	config := providerBlock(f) + `
resource "nextdns_parental_control" "this" {
  profile_id              = "abc123"
  safe_search             = false
  youtube_restricted_mode = false
  block_bypass            = true

  service {
    id         = "tiktok"
    active     = true
    recreation = false
  }

  recreation {
    timezone = ""
  }
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV5ProviderFactories: protoV5ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("nextdns_parental_control.this", "block_bypass", "true"),
					resource.TestCheckResourceAttr("nextdns_parental_control.this", "service.#", "1"),
					func(_ *terraformState) error {
						reqs := f.requests("PATCH", "/parentalControl")
						if len(reqs) == 0 {
							return fmt.Errorf("expected a PATCH /parentalControl")
						}
						for _, r := range reqs {
							for _, k := range []string{"services", "categories"} {
								if _, present := r.Body[k]; present {
									return fmt.Errorf("PATCH /parentalControl carried %q: %v", k, r.Body)
								}
							}
						}
						if len(f.requests("PUT", "/parentalControl/services")) == 0 {
							return fmt.Errorf("services were not written through their own endpoint")
						}
						// The declared block is empty and so is the live
						// schedule: nothing to write.
						if _, present := reqs[len(reqs)-1].Body["recreation"]; present {
							return fmt.Errorf("PATCH /parentalControl carried an empty recreation: %v", reqs[len(reqs)-1].Body)
						}
						return nil
					},
				),
			},
			{
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

func scheduleConfig(f *fakeAPI, monday string) string {
	return providerBlock(f) + fmt.Sprintf(`
resource "nextdns_parental_control" "this" {
  profile_id              = "abc123"
  safe_search             = true
  youtube_restricted_mode = true
  block_bypass            = false

  recreation {
    timezone = "Europe/London"
    monday {
      start = "18:00:00"
      end   = %q
    }
  }
}
`, monday)
}

// A schedule change is written and read back; a malformed time is refused
// before anything is sent.
func TestParentalControl_ScheduleUpdateAndValidation(t *testing.T) {
	f := newFakeAPI(t)
	f.seed("abc123", "parentalControl", `{"safeSearch": false, "youtubeRestrictedMode": false, "blockBypass": true}`)
	f.seed("abc123", "parentalControl/services", `[]`)
	f.seed("abc123", "parentalControl/categories", `[]`)

	resource.UnitTest(t, resource.TestCase{
		ProtoV5ProviderFactories: protoV5ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: scheduleConfig(f, "20:30:00"),
				Check:  resource.TestCheckResourceAttr("nextdns_parental_control.this", "recreation.0.monday.0.end", "20:30:00"),
			},
			{
				Config: scheduleConfig(f, "21:00:00"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("nextdns_parental_control.this", plancheck.ResourceActionUpdate),
					},
				},
				Check: func(_ *terraformState) error {
					rec, _ := f.get("abc123", "parentalControl", "recreation")
					times, _ := rec.(map[string]any)["times"].(map[string]any)
					monday, _ := times["monday"].(map[string]any)
					if monday["end"] != "21:00:00" {
						return fmt.Errorf("API monday = %v, want end 21:00:00", monday)
					}
					return nil
				},
			},
		},
	})

	// Separate case: the harness destroys with the last step's config.
	resource.UnitTest(t, resource.TestCase{
		ProtoV5ProviderFactories: protoV5ProviderFactories(),
		Steps: []resource.TestStep{{
			Config:      scheduleConfig(f, "25:00:00"),
			ExpectError: regexp.MustCompile(`Must be in HH:MM:00 format`),
		}},
	})
}

// seedParentalWithoutSchedule seeds a profile with one category and no
// recreation schedule, in the shape the API returns for one: timezone null
// and empty times.
func seedParentalWithoutSchedule(f *fakeAPI) {
	f.seed("abc123", "parentalControl", `{"safeSearch": false, "youtubeRestrictedMode": false, "blockBypass": true, "recreation": {"times": {}, "timezone": null}}`)
	f.seed("abc123", "parentalControl/services", `[]`)
	f.seed("abc123", "parentalControl/categories", `[{"id": "gambling", "active": true, "recreation": false}]`)
}

// A profile without a schedule imports with no recreation block and plans no
// changes, and an update leaves recreation out of the PATCH, so no empty
// timezone is ever written.
func TestParentalControl_NoScheduleIsNoBlock(t *testing.T) {
	f := newFakeAPI(t)
	seedParentalWithoutSchedule(f)
	changed := strings.Replace(parentalConfig(f, ""), "safe_search             = false", "safe_search             = true", 1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV5ProviderFactories: protoV5ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config:             parentalConfig(f, ""),
				ResourceName:       "nextdns_parental_control.this",
				ImportState:        true,
				ImportStateId:      "abc123",
				ImportStatePersist: true,
				ImportStateCheck: func(states []*instanceState) error {
					if got := states[0].Attributes["recreation.#"]; got != "0" {
						return fmt.Errorf("recreation.# = %q after import, want 0", got)
					}
					return nil
				},
			},
			{
				Config: parentalConfig(f, ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				Config: changed,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("nextdns_parental_control.this", plancheck.ResourceActionUpdate),
				}},
				Check: func(_ *terraformState) error {
					reqs := f.requests("PATCH", "/parentalControl")
					if len(reqs) != 1 {
						return fmt.Errorf("expected one PATCH /parentalControl, got %d", len(reqs))
					}
					if _, present := reqs[0].Body["recreation"]; present {
						return fmt.Errorf("PATCH /parentalControl carried recreation without a declared schedule: %v", reqs[0].Body)
					}
					rec, _ := f.get("abc123", "parentalControl", "recreation")
					if tz := rec.(map[string]any)["timezone"]; tz != nil {
						return fmt.Errorf("live timezone = %v, want null", tz)
					}
					return nil
				},
			},
			{
				Config: changed,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

// Removing a declared schedule clears it on the profile, after which there is
// no block and no diff.
func TestParentalControl_RemovingTheScheduleClearsIt(t *testing.T) {
	f := newFakeAPI(t)
	f.seed("abc123", "parentalControl", `{"safeSearch": false, "youtubeRestrictedMode": false, "blockBypass": true}`)
	f.seed("abc123", "parentalControl/services", `[]`)
	f.seed("abc123", "parentalControl/categories", `[]`)
	noSchedule := strings.Replace(scheduleConfig(f, "20:30:00"), `  recreation {
    timezone = "Europe/London"
    monday {
      start = "18:00:00"
      end   = "20:30:00"
    }
  }
`, "", 1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV5ProviderFactories: protoV5ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: scheduleConfig(f, "20:30:00"),
				Check:  resource.TestCheckResourceAttr("nextdns_parental_control.this", "recreation.0.timezone", "Europe/London"),
			},
			{
				Config: noSchedule,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("nextdns_parental_control.this", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("nextdns_parental_control.this", "recreation.#", "0"),
					func(_ *terraformState) error {
						reqs := f.requests("PATCH", "/parentalControl")
						last := reqs[len(reqs)-1].Body
						rec, ok := last["recreation"].(map[string]any)
						if !ok || rec["timezone"] != "" || rec["times"] != nil {
							return fmt.Errorf("last PATCH did not clear the schedule: %v", last)
						}
						return nil
					},
				),
			},
			{
				Config: noSchedule,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}
