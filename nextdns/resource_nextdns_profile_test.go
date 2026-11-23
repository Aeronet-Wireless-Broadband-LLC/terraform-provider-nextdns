// editorconfig-checker-disable-file: HCL in raw strings is space-indented.
package nextdns

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

func profileConfig(f *fakeAPI, name, extra string) string {
	return providerBlock(f) + fmt.Sprintf(`
resource "nextdns_profile" "this" {
  name = %q
%s
}
`, name, extra)
}

const unprotected = `  deletion_protection = false`

// Create, rename in place, import, and destroy against the fake API, with
// deletion protection turned off.
func TestProfile_Lifecycle(t *testing.T) {
	f := newFakeAPI(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV5ProviderFactories: protoV5ProviderFactories(),
		CheckDestroy: func(_ *terraformState) error {
			if len(f.requests("DELETE", "/profiles/fake01")) != 1 {
				return fmt.Errorf("expected one DELETE /profiles/fake01")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: profileConfig(f, "first", unprotected),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("nextdns_profile.this", "id", "fake01"),
					resource.TestCheckResourceAttr("nextdns_profile.this", "profile_id", "fake01"),
					resource.TestCheckResourceAttr("nextdns_profile.this", "name", "first"),
					resource.TestCheckResourceAttr("nextdns_profile.this", "deletion_protection", "false"),
				),
			},
			{
				Config: profileConfig(f, "second", unprotected),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("nextdns_profile.this", plancheck.ResourceActionUpdate),
					},
				},
				Check: func(_ *terraformState) error {
					reqs := f.requests("PATCH", "/profiles/fake01")
					if len(reqs) != 1 || reqs[0].Body["name"] != "second" {
						return fmt.Errorf("expected one PATCH renaming to second, got %v", reqs)
					}
					if _, sent := reqs[0].Body["deletion_protection"]; sent {
						return fmt.Errorf("deletion_protection was sent to the API: %v", reqs[0].Body)
					}
					return nil
				},
			},
			{
				// deletion_protection lives only in state; an import records
				// the default.
				Config:                  profileConfig(f, "second", unprotected),
				ResourceName:            "nextdns_profile.this",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"deletion_protection"},
			},
		},
	})
}

// By default destroy refuses to delete the profile and sends nothing. Turning
// the protection off is an in-place update, after which destroy deletes it.
func TestProfile_DeletionProtection(t *testing.T) {
	f := newFakeAPI(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV5ProviderFactories: protoV5ProviderFactories(),
		CheckDestroy: func(_ *terraformState) error {
			if n := len(f.requests("DELETE", "/profiles/fake01")); n != 1 {
				return fmt.Errorf("expected one DELETE /profiles/fake01 once unprotected, got %d", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: profileConfig(f, "protected", ""),
				Check:  resource.TestCheckResourceAttr("nextdns_profile.this", "deletion_protection", "true"),
			},
			{
				// An imported profile is protected and plans no changes.
				Config:            profileConfig(f, "protected", ""),
				ResourceName:      "nextdns_profile.this",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config:      profileConfig(f, "protected", ""),
				Destroy:     true,
				ExpectError: regexp.MustCompile(`Profile is protected from deletion`),
			},
			{
				Config: profileConfig(f, "protected", unprotected),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("nextdns_profile.this", plancheck.ResourceActionUpdate),
					},
				},
				Check: func(_ *terraformState) error {
					if n := len(f.requests("DELETE", "/profiles/fake01")); n != 0 {
						return fmt.Errorf("a protected profile was deleted (%d DELETEs)", n)
					}
					if _, ok := f.get("fake01", "", "name"); !ok {
						return fmt.Errorf("the protected profile is gone from the API")
					}
					return nil
				},
			},
		},
	})
}
