resource "nextdns_parental_control" "this" {
  profile_id = nextdns_profile.this.id

  safe_search             = false
  youtube_restricted_mode = false
  block_bypass            = true

  service {
    id         = "tiktok"
    active     = true
    recreation = false
  }

  category {
    id         = "gambling"
    active     = true
    recreation = false
  }

  # Optional: leave the block out for a profile without a recreation
  # schedule. Removing a declared schedule clears it.
  recreation {
    timezone = "Europe/London"

    saturday {
      start = "10:00:00"
      end   = "20:00:00"
    }
  }
}
