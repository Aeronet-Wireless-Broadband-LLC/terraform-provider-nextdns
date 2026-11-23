resource "nextdns_profile" "this" {
  name = "terraform-provider-nextdns"

  # Defaults to true: destroy fails instead of deleting the live profile.
  # Set it to false and apply before a destroy that should delete it.
  deletion_protection = true
}
