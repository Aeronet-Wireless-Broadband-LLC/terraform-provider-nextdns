resource "nextdns_allowlist" "this" {
  profile_id = nextdns_profile.this.id

  # Destroy only removes the resource from state. With reset_on_destroy = true
  # (applied before the destroy) it empties the live list instead.
  reset_on_destroy = false

  domain {
    id     = "nextdns.io"
    active = true
  }

  domain {
    id     = "example.com"
    active = false
  }
}
