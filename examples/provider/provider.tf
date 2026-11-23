terraform {
  required_providers {
    nextdns = {
      source  = "aeronet-wireless-broadband-llc/nextdns"
      version = "~> 0.7"
    }
  }
}

# The API key can also come from the NEXTDNS_API_KEY environment variable.
provider "nextdns" {
  api_key = var.nextdns_api_key
}
