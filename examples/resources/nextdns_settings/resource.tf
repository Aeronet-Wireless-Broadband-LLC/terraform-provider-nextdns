resource "nextdns_settings" "this" {
  profile_id = nextdns_profile.this.id

  logs {
    enabled = true

    privacy {
      log_clients_ip = true
      log_domains    = true
    }

    retention = "3 months"
    location  = "ch"
  }

  block_page {
    enabled = false
  }

  performance {
    ecs              = true
    cache_boost      = true
    cname_flattening = false
  }

  web3 = true

  # Bypass Age Verification (v0.3.0). Optional + Computed: omit it to keep
  # the live value.
  bypass_age_verification = true

  # settings.csam, separate from nextdns_security.csam. Optional + Computed:
  # omit it to keep the live value.
  csam = true
}
