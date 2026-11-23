// editorconfig-checker-disable-file: HCL in raw strings is space-indented.
package nextdns

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

// seedSettings mirrors the live shape of both managed profiles.
func seedSettings(f *fakeAPI, bav string) {
	f.seed("abc123", "settings", `{"web3": true`+bav+`}`)
	f.seed("abc123", "settings/logs", `{"enabled": true, "drop": {"ip": false, "domain": false}, "retention": 7776000, "location": "ch"}`)
	f.seed("abc123", "settings/blockPage", `{"enabled": false}`)
	f.seed("abc123", "settings/performance", `{"ecs": true, "cacheBoost": true, "cnameFlattening": false}`)
}

func settingsConfig(f *fakeAPI, extra string) string {
	return providerBlock(f) + fmt.Sprintf(`
resource "nextdns_settings" "this" {
  profile_id = "abc123"
  web3       = true
%s
  logs {
    enabled   = true
    location  = "ch"
    retention = "3 months"
    privacy {
      log_clients_ip = true
      log_domains    = true
    }
  }
  block_page {
    enabled = false
  }
  performance {
    ecs              = true
    cache_boost      = true
    cname_flattening = false
  }
}
`, extra)
}

func TestSettings_OmittedBavPreservesLive(t *testing.T) {
	f := newFakeAPI(t)
	seedSettings(f, `, "bav": true`)

	resource.UnitTest(t, resource.TestCase{
		ProtoV5ProviderFactories: protoV5ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: settingsConfig(f, ""),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("nextdns_settings.this", "bypass_age_verification", "true"),
					resource.TestCheckResourceAttr("nextdns_settings.this", "web3", "true"),
					func(_ *terraformState) error {
						reqs := f.requests("PATCH", "/settings")
						if len(reqs) == 0 {
							return fmt.Errorf("expected a PATCH /settings")
						}
						for _, r := range reqs {
							if _, present := r.Body["bav"]; present {
								return fmt.Errorf("PATCH /settings sent bav although omitted: %v", r.Body)
							}
						}
						if v, _ := f.get("abc123", "settings", "bav"); v != true {
							return fmt.Errorf("API bav changed to %v", v)
						}
						return nil
					},
				),
			},
			{
				Config: settingsConfig(f, ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

func TestSettings_ExplicitBavIsWrittenBothWays(t *testing.T) {
	f := newFakeAPI(t)
	seedSettings(f, `, "bav": true`)

	resource.UnitTest(t, resource.TestCase{
		ProtoV5ProviderFactories: protoV5ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: settingsConfig(f, `  bypass_age_verification = false`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("nextdns_settings.this", "bypass_age_verification", "false"),
					func(_ *terraformState) error {
						if v, _ := f.get("abc123", "settings", "bav"); v != false {
							return fmt.Errorf("API bav = %v, want false", v)
						}
						return nil
					},
				),
			},
			{
				Config: settingsConfig(f, `  bypass_age_verification = true`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("nextdns_settings.this", plancheck.ResourceActionUpdate),
					},
				},
				Check: func(_ *terraformState) error {
					if v, _ := f.get("abc123", "settings", "bav"); v != true {
						return fmt.Errorf("API bav = %v, want true", v)
					}
					return nil
				},
			},
			{
				Config: settingsConfig(f, `  bypass_age_verification = true`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

func TestSettings_BavAbsentFromResponseKeepsState(t *testing.T) {
	f := newFakeAPI(t)
	seedSettings(f, `, "bav": true`)

	resource.UnitTest(t, resource.TestCase{
		ProtoV5ProviderFactories: protoV5ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: settingsConfig(f, `  bypass_age_verification = true`),
				Check:  resource.TestCheckResourceAttr("nextdns_settings.this", "bypass_age_verification", "true"),
			},
			{
				PreConfig: func() { f.hide("abc123", "settings", "bav") },
				Config:    settingsConfig(f, `  bypass_age_verification = true`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.TestCheckResourceAttr("nextdns_settings.this", "bypass_age_verification", "true"),
			},
		},
	})
}

func TestSettings_ImportReadsBav(t *testing.T) {
	f := newFakeAPI(t)
	seedSettings(f, `, "bav": true`)

	resource.UnitTest(t, resource.TestCase{
		ProtoV5ProviderFactories: protoV5ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config:             settingsConfig(f, ""),
				ResourceName:       "nextdns_settings.this",
				ImportState:        true,
				ImportStateId:      "abc123",
				ImportStatePersist: true,
				ImportStateCheck: func(states []*instanceState) error {
					if got := states[0].Attributes["bypass_age_verification"]; got != "true" {
						return fmt.Errorf("bypass_age_verification = %q, want true", got)
					}
					return nil
				},
			},
		},
	})
}

// With ignore_changes on bypass_age_verification, an update to another
// attribute must send the planned value (the live one), not the one in config.
func TestSettings_IgnoreChangesSendsPlannedBav(t *testing.T) {
	f := newFakeAPI(t)
	seedSettings(f, `, "bav": false`)

	ignored := `  bypass_age_verification = false
  lifecycle {
    ignore_changes = [bypass_age_verification]
  }
`
	web3Off := strings.Replace(settingsConfig(f, ignored), "web3       = true", "web3       = false", 1)
	resource.UnitTest(t, resource.TestCase{
		ProtoV5ProviderFactories: protoV5ProviderFactories(),
		Steps: []resource.TestStep{
			{Config: settingsConfig(f, ignored)},
			{
				PreConfig: func() { f.seed("abc123", "settings", `{"bav": true}`) },
				Config:    web3Off,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("nextdns_settings.this", "bypass_age_verification", "true"),
					func(_ *terraformState) error {
						if v, _ := f.get("abc123", "settings", "bav"); v != true {
							return fmt.Errorf("API bav = %v, want true: the update overwrote an ignored value", v)
						}
						if v, _ := f.get("abc123", "settings", "web3"); v != false {
							return fmt.Errorf("API web3 = %v, want false", v)
						}
						return nil
					},
				),
			},
		},
	})
}

// csam (API settings.csam) has the same three-way behaviour as
// bypass_age_verification: omitted is not sent and records the live value,
// an explicit value is written either way, and the API no longer returning
// it keeps the last known value.
func TestSettings_CsamThreeWay(t *testing.T) {
	f := newFakeAPI(t)
	seedSettings(f, `, "bav": false, "csam": true`)

	csamNotSent := func(_ *terraformState) error {
		reqs := f.requests("PATCH", "/settings")
		if len(reqs) == 0 {
			return fmt.Errorf("expected a PATCH /settings")
		}
		for _, r := range reqs {
			if _, present := r.Body["csam"]; present {
				return fmt.Errorf("PATCH /settings sent csam although omitted: %v", r.Body)
			}
		}
		if v, _ := f.get("abc123", "settings", "csam"); v != true {
			return fmt.Errorf("API csam changed to %v", v)
		}
		return nil
	}
	apiCsam := func(want bool) resource.TestCheckFunc {
		return func(_ *terraformState) error {
			if v, _ := f.get("abc123", "settings", "csam"); v != want {
				return fmt.Errorf("API csam = %v, want %t", v, want)
			}
			return nil
		}
	}
	empty := resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}

	resource.UnitTest(t, resource.TestCase{
		ProtoV5ProviderFactories: protoV5ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: settingsConfig(f, ""),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("nextdns_settings.this", "csam", "true"),
					csamNotSent,
				),
			},
			{Config: settingsConfig(f, ""), ConfigPlanChecks: empty},
			{
				Config: settingsConfig(f, `  csam = false`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("nextdns_settings.this", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("nextdns_settings.this", "csam", "false"),
					apiCsam(false),
				),
			},
			{
				Config: settingsConfig(f, `  csam = true`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("nextdns_settings.this", "csam", "true"),
					apiCsam(true),
				),
			},
			{
				PreConfig:        func() { f.hide("abc123", "settings", "csam") },
				Config:           settingsConfig(f, `  csam = true`),
				ConfigPlanChecks: empty,
				Check:            resource.TestCheckResourceAttr("nextdns_settings.this", "csam", "true"),
			},
		},
	})
}

func TestSettings_ImportReadsCsam(t *testing.T) {
	f := newFakeAPI(t)
	seedSettings(f, `, "bav": false, "csam": true`)

	resource.UnitTest(t, resource.TestCase{
		ProtoV5ProviderFactories: protoV5ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config:             settingsConfig(f, ""),
				ResourceName:       "nextdns_settings.this",
				ImportState:        true,
				ImportStateId:      "abc123",
				ImportStatePersist: true,
				ImportStateCheck: func(states []*instanceState) error {
					if got := states[0].Attributes["csam"]; got != "true" {
						return fmt.Errorf("csam = %q, want true", got)
					}
					return nil
				},
			},
			{
				Config: settingsConfig(f, ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}
