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

// profileIDLine matches the profile_id line of a test configuration.
var profileIDLine = regexp.MustCompile(`(?m)^(\s*profile_id\s*=\s*"abc123")$`)

// withAttr adds an attribute line right after the resource's profile_id.
func withAttr(cfg, attr string) string {
	return profileIDLine.ReplaceAllString(cfg, "${1}\n  "+attr)
}

func parentalConfig(f *fakeAPI, extra string) string {
	return providerBlock(f) + fmt.Sprintf(`
resource "nextdns_parental_control" "this" {
  profile_id              = "abc123"
  safe_search             = false
  youtube_restricted_mode = false
  block_bypass            = true

  category {
    id         = "gambling"
    active     = true
    recreation = false
  }

  recreation {
    timezone = ""
  }
%s
}
`, extra)
}

func denylistConfig(f *fakeAPI, domains ...string) string {
	return strings.Replace(allowlistConfig(f, domains...), "nextdns_allowlist", "nextdns_denylist", 1)
}

// sectionCase is one profile-section resource: how to seed the fake API, a
// configuration for it, and a check that passes only when destroy has reset
// the live section.
type sectionCase struct {
	address string
	seed    func(f *fakeAPI)
	config  func(f *fakeAPI) string
	isReset func(f *fakeAPI) error
}

func sectionCases() []sectionCase {
	return []sectionCase{
		{
			address: "nextdns_security.this",
			seed: func(f *fakeAPI) {
				f.seed("abc123", "security", securityLive)
				f.seed("abc123", "security/tlds", `[]`)
			},
			config: func(f *fakeAPI) string { return securityConfig(f, "") },
			isReset: func(f *fakeAPI) error {
				if l := f.list("abc123", "security/tlds"); len(l) != 0 {
					return fmt.Errorf("security tlds = %v, want empty", l)
				}
				if v, _ := f.get("abc123", "security", "threatIntelligenceFeeds"); v != false {
					return fmt.Errorf("threatIntelligenceFeeds = %v, want false", v)
				}
				return nil
			},
		},
		{
			address: "nextdns_privacy.this",
			seed:    seedPrivacy,
			config:  func(f *fakeAPI) string { return privacyConfig(f, `  natives = ["apple"]`) },
			isReset: func(f *fakeAPI) error {
				if l := f.list("abc123", "privacy/natives"); len(l) != 0 {
					return fmt.Errorf("privacy natives = %v, want empty", l)
				}
				if v, _ := f.get("abc123", "privacy", "disguisedTrackers"); v != false {
					return fmt.Errorf("disguisedTrackers = %v, want false", v)
				}
				return nil
			},
		},
		{
			address: "nextdns_parental_control.this",
			seed: func(f *fakeAPI) {
				f.seed("abc123", "parentalControl", `{"safeSearch": false, "youtubeRestrictedMode": false, "blockBypass": false}`)
				f.seed("abc123", "parentalControl/services", `[]`)
				f.seed("abc123", "parentalControl/categories", `[]`)
			},
			config: func(f *fakeAPI) string { return parentalConfig(f, "") },
			isReset: func(f *fakeAPI) error {
				if l := f.list("abc123", "parentalControl/categories"); len(l) != 0 {
					return fmt.Errorf("parental control categories = %v, want empty", l)
				}
				if v, _ := f.get("abc123", "parentalControl", "blockBypass"); v != false {
					return fmt.Errorf("blockBypass = %v, want false", v)
				}
				return nil
			},
		},
		{
			address: "nextdns_settings.this",
			seed:    func(f *fakeAPI) { seedSettings(f, "") },
			config:  func(f *fakeAPI) string { return settingsConfig(f, "") },
			isReset: func(f *fakeAPI) error {
				if v, _ := f.get("abc123", "settings/logs", "enabled"); v != false {
					return fmt.Errorf("logs.enabled = %v, want false", v)
				}
				if v, _ := f.get("abc123", "settings", "web3"); v != false {
					return fmt.Errorf("web3 = %v, want false", v)
				}
				return nil
			},
		},
		{
			address: "nextdns_allowlist.this",
			seed:    func(f *fakeAPI) { f.seed("abc123", "allowlist", `[]`) },
			config:  func(f *fakeAPI) string { return allowlistConfig(f, "a.example") },
			isReset: func(f *fakeAPI) error {
				if l := f.list("abc123", "allowlist"); len(l) != 0 {
					return fmt.Errorf("allowlist = %v, want empty", l)
				}
				return nil
			},
		},
		{
			address: "nextdns_denylist.this",
			seed:    func(f *fakeAPI) { f.seed("abc123", "denylist", `[]`) },
			config:  func(f *fakeAPI) string { return denylistConfig(f, "a.example") },
			isReset: func(f *fakeAPI) error {
				if l := f.list("abc123", "denylist"); len(l) != 0 {
					return fmt.Errorf("denylist = %v, want empty", l)
				}
				return nil
			},
		},
		{
			address: "nextdns_rewrite.this",
			seed:    func(f *fakeAPI) { f.seed("abc123", "rewrites", `[]`) },
			config:  func(f *fakeAPI) string { return rewriteConfig(f, [2]string{"a.example", "192.0.2.1"}) },
			isReset: func(f *fakeAPI) error {
				if l := f.list("abc123", "rewrites"); len(l) != 0 {
					return fmt.Errorf("rewrites = %v, want empty", l)
				}
				return nil
			},
		},
	}
}

// By default destroying a section resource only forgets it: no write reaches
// the API and the live section keeps its values.
func TestSections_DestroyOnlyRemovesFromState(t *testing.T) {
	for _, tc := range sectionCases() {
		t.Run(tc.address, func(t *testing.T) {
			f := newFakeAPI(t)
			tc.seed(f)
			writesBeforeDestroy := -1

			resource.UnitTest(t, resource.TestCase{
				ProtoV5ProviderFactories: protoV5ProviderFactories(),
				CheckDestroy: func(_ *terraformState) error {
					if n := f.writes(); n != writesBeforeDestroy {
						return fmt.Errorf("destroy sent %d write request(s) to the API", n-writesBeforeDestroy)
					}
					if tc.isReset(f) == nil {
						return fmt.Errorf("the live section was reset on destroy")
					}
					return nil
				},
				Steps: []resource.TestStep{{
					Config: tc.config(f),
					Check: resource.ComposeTestCheckFunc(
						resource.TestCheckResourceAttr(tc.address, "reset_on_destroy", "false"),
						func(_ *terraformState) error {
							writesBeforeDestroy = f.writes()
							return nil
						},
					),
				}},
			})
		})
	}
}

// reset_on_destroy = true, applied before the destroy, restores the old
// behaviour: destroy resets the live section.
func TestSections_ResetOnDestroyResetsTheLiveSection(t *testing.T) {
	for _, tc := range sectionCases() {
		t.Run(tc.address, func(t *testing.T) {
			f := newFakeAPI(t)
			tc.seed(f)

			resource.UnitTest(t, resource.TestCase{
				ProtoV5ProviderFactories: protoV5ProviderFactories(),
				CheckDestroy:             func(_ *terraformState) error { return tc.isReset(f) },
				Steps: []resource.TestStep{
					{Config: tc.config(f)},
					{
						Config: withAttr(tc.config(f), "reset_on_destroy = true"),
						ConfigPlanChecks: resource.ConfigPlanChecks{
							PreApply: []plancheck.PlanCheck{
								plancheck.ExpectResourceAction(tc.address, plancheck.ResourceActionUpdate),
							},
						},
						Check: resource.ComposeTestCheckFunc(
							resource.TestCheckResourceAttr(tc.address, "reset_on_destroy", "true"),
							func(_ *terraformState) error {
								if tc.isReset(f) == nil {
									return fmt.Errorf("the live section was reset before destroy")
								}
								return nil
							},
						),
					},
				},
			})
		})
	}
}

// importCase is a live profile section and the configuration that describes
// it exactly, as it would be written for an existing production profile.
type importCase struct {
	address string
	seed    func(f *fakeAPI)
	config  func(f *fakeAPI) string
}

func importCases() []importCase {
	return []importCase{
		{
			address: "nextdns_security.this",
			seed: func(f *fakeAPI) {
				f.seed("abc123", "security", securityLive)
				f.seed("abc123", "security/tlds", `[{"id":"autos"},{"id":"bid"}]`)
			},
			config: func(f *fakeAPI) string { return securityConfig(f, "") },
		},
		{
			address: "nextdns_privacy.this",
			seed: func(f *fakeAPI) {
				f.seed("abc123", "privacy", `{"disguisedTrackers": true, "allowAffiliate": false, "rotatingAdDomains": false}`)
				f.seed("abc123", "privacy/blocklists", `[{"id":"nextdns-recommended","name":null,"entries":1,"updatedOn":"2026-01-01T00:00:00.000Z"}]`)
				f.seed("abc123", "privacy/natives", `[]`)
			},
			config: func(f *fakeAPI) string { return privacyConfig(f, `  blocklists = ["nextdns-recommended"]`) },
		},
		{
			address: "nextdns_settings.this",
			seed:    func(f *fakeAPI) { seedSettings(f, `, "bav": false`) },
			config:  func(f *fakeAPI) string { return settingsConfig(f, "") },
		},
		{
			address: "nextdns_allowlist.this",
			seed: func(f *fakeAPI) {
				f.seed("abc123", "allowlist", `[{"id":"a.example","active":true},{"id":"b.example","active":true}]`)
			},
			config: func(f *fakeAPI) string { return allowlistConfig(f, "b.example", "a.example") },
		},
		{
			address: "nextdns_denylist.this",
			seed:    func(f *fakeAPI) { f.seed("abc123", "denylist", `[{"id":"a.example","active":true}]`) },
			config:  func(f *fakeAPI) string { return denylistConfig(f, "a.example") },
		},
		{
			address: "nextdns_rewrite.this",
			seed: func(f *fakeAPI) {
				f.seed("abc123", "rewrites", `[{"id":"rw01","name":"a.example","type":"A","content":"192.0.2.1"},{"id":"rw02","name":"a.example","type":"A","content":"192.0.2.2"}]`)
			},
			config: func(f *fakeAPI) string {
				return rewriteConfig(f, [2]string{"a.example", "192.0.2.2"}, [2]string{"a.example", "192.0.2.1"})
			},
		},
	}
}

// Importing an existing profile section and planning the configuration that
// describes it gives an empty plan: the new state-only attributes take their
// defaults without a diff.
func TestSections_ImportPlansNoChanges(t *testing.T) {
	for _, tc := range importCases() {
		t.Run(tc.address, func(t *testing.T) {
			f := newFakeAPI(t)
			tc.seed(f)
			writes := 0

			resource.UnitTest(t, resource.TestCase{
				ProtoV5ProviderFactories: protoV5ProviderFactories(),
				Steps: []resource.TestStep{
					{
						Config:             tc.config(f),
						ResourceName:       tc.address,
						ImportState:        true,
						ImportStateId:      "abc123",
						ImportStatePersist: true,
						ImportStateCheck: func(states []*instanceState) error {
							if len(states) != 1 {
								return fmt.Errorf("expected 1 state, got %d", len(states))
							}
							if got := states[0].Attributes["reset_on_destroy"]; got != "false" {
								return fmt.Errorf("reset_on_destroy = %q after import, want false", got)
							}
							writes = f.writes()
							return nil
						},
					},
					{
						Config: tc.config(f),
						ConfigPlanChecks: resource.ConfigPlanChecks{
							PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
						},
						Check: func(_ *terraformState) error {
							if n := f.writes(); n != writes {
								return fmt.Errorf("import and plan sent %d write request(s)", n-writes)
							}
							return nil
						},
					},
				},
			})
		})
	}
}

// An existing profile imports protected and plans no changes.
func TestProfile_ImportPlansNoChanges(t *testing.T) {
	f := newFakeAPI(t)
	f.seed("prof01", "", `{"name": "imported"}`)

	resource.UnitTest(t, resource.TestCase{
		ProtoV5ProviderFactories: protoV5ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config:             profileConfig(f, "imported", ""),
				ResourceName:       "nextdns_profile.this",
				ImportState:        true,
				ImportStateId:      "prof01",
				ImportStatePersist: true,
				ImportStateCheck: func(states []*instanceState) error {
					if got := states[0].Attributes["deletion_protection"]; got != "true" {
						return fmt.Errorf("deletion_protection = %q after import, want true", got)
					}
					return nil
				},
			},
			{
				Config: profileConfig(f, "imported", ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// Unprotect so that the harness's final destroy can run.
				Config: profileConfig(f, "imported", unprotected),
			},
		},
	})
}
