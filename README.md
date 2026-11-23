# terraform-provider-nextdns

[NextDNS](https://nextdns.io/) provider for [OpenTofu](https://opentofu.org/) and [Terraform](https://terraform.io).

This is AeroNet's `aeronet-wireless-broadband-llc/nextdns` fork, based on [`LukeEvansTech/terraform-provider-nextdns`](https://github.com/LukeEvansTech/terraform-provider-nextdns) (v0.6.0), itself a fork of [`amalucelli/terraform-provider-nextdns`](https://github.com/amalucelli/terraform-provider-nextdns), which has not had a release since v0.2.0 (February 2024). The LukeEvansTech fork:

- adds the fourteen profile switches the API exposes but upstream cannot manage:
  - twelve security switches: `free_hosting_domains`, `tunneling_endpoints`, `data_drop_services`, `residential_hosting`, `untrusted_certificates`, `fast_flux_networks`, `dns_data_exfiltration`, `dns_payload_delivery`, `decentralized_web_gateways`, `high_risk_tlds`, `newly_active_domains` and `algorithmically_generated_domains`;
  - `rotating_ad_domains` (`privacy.rotatingAdDomains`);
  - `bypass_age_verification` (`settings.bav`);
- keeps every upstream resource, attribute and identifier unchanged, so an existing state moves across with `state replace-provider` and a no-change plan;
- folds the [`nextdns-go`](https://github.com/amalucelli/nextdns-go) client in as `internal/nextdns` (upstream last released March 2023);
- ports the parental-control fix from [`cozy-corner`](https://github.com/cozy-corner/terraform-provider-nextdns) (services and categories are no longer resent in the profile PATCH; a recreation block with only a timezone no longer panics);
- adds a test suite driven by OpenTofu against an in-memory stub of the NextDNS API, plus an opt-in live acceptance test.

On top of that, this fork makes the provider safe to point at production profiles (details below):

- destroying a resource no longer changes the live profile unless you opt in, and `nextdns_profile` is protected from deletion by default;
- the API client retries rate-limited and failed requests and no longer panics on a 5xx or non-JSON response;
- the deprecated `dga`, `typo_squatting` and `idn_homographs` switches are never sent;
- `nextdns_settings.csam` is managed; parental control without a recreation schedule imports cleanly;
- TLDs, blocklists and natives are sets, and allowlists, denylists and rewrites may be empty.

Upstream's history is preserved in this repository.

## Installation

Published on the [OpenTofu registry](https://search.opentofu.org/) from this repository's GitHub Releases (signed `SHA256SUMS`).

```hcl
terraform {
  required_providers {
    nextdns = {
      source  = "aeronet-wireless-broadband-llc/nextdns"
      version = "~> 0.7"
    }
  }
}

provider "nextdns" {
  api_key = var.nextdns_api_key # or NEXTDNS_API_KEY
}
```

## Destroy behaviour

Destroying a resource does not change the live profile unless you opt in. A resource is destroyed by `tofu destroy`, by removing or renaming its block without a `moved` block, or by a `for_each` or `count` change.

| Resource | Destroy by default | Opt-in |
| -------- | ------------------ | ------ |
| `nextdns_security`, `nextdns_privacy`, `nextdns_parental_control`, `nextdns_settings`, `nextdns_allowlist`, `nextdns_denylist`, `nextdns_rewrite` | removes the resource from state only, with a warning; the live section keeps its values | `reset_on_destroy = true` (default `false`): destroy resets the section as earlier releases always did: protections off, lists emptied, every rewrite on the profile deleted, logging off |
| `nextdns_profile` | fails with an error; nothing is deleted | `deletion_protection = false` (default `true`): destroy deletes the profile, and every device using it loses its configuration |

Both attributes exist only in state, and destroy reads them from state, so change them and apply before destroying. To stop managing a resource without touching the profile, use a `removed` block with `lifecycle { destroy = false }` (or `tofu state rm`). State written by earlier releases picks up the defaults on the next refresh without a plan change.

## Import

Every resource imports by profile ID, for example `tofu import nextdns_security.this <profile-id>` or an `import` block with `id = "<profile-id>"`. Import existing sections rather than creating them: create writes the configuration over the live section. A configuration that describes the live profile plans no changes after import. Lists, rewrites, categories and services are authoritative: entries added in the dashboard show up as drift and are removed by the next apply.

## API client

- 429 and 5xx responses are retried with exponential backoff and jitter, at most 4 attempts, waiting at least as long as `Retry-After` asks (a `Retry-After` over a minute returns the error instead). GET, PUT, PATCH and DELETE are retried on 429 and 5xx; POST (rewrite creation) only on 429, because a POST that failed with a 5xx may have been applied.
- Redirects are never followed, so the API key cannot be sent anywhere else.
- `base_url` / `NEXTDNS_BASE_URL` is only for stub servers: it must be `https://api.nextdns.io/` or a loopback URL (`http://127.0.0.1:<port>/`, `http://localhost:<port>/`); anything else is a configuration error.

## The new attributes

The fourteen switches above, plus `csam` on `nextdns_settings` (API `settings.csam`, separate from `nextdns_security.csam`), are `Optional + Computed` booleans with the same three-way behaviour:

| Written in configuration | What the provider does                                                  |
| ------------------------ | ----------------------------------------------------------------------- |
| omitted                  | sends nothing for it; the live value is preserved and recorded in state |
| `false` or `true`        | writes that value                                                       |
| (API stops returning it) | state keeps the last known value; no diff                               |

`fast_flux_networks` and `dns_data_exfiltration` are returned by the API for every profile inspected but hidden in the dashboard for some. The API accepts them on read; whether a write takes effect on a given profile has not been verified. Leave them omitted until it has been on yours.

`dga`, `typo_squatting` and `idn_homographs` are deprecated by NextDNS, which ignores writes to them and always returns `false`. They are `Optional + Computed` with a deprecation warning and are never sent: leave them out. Configurations that still set them to `false` plan cleanly.

## Other schema notes

- `nextdns_security.tlds`, `nextdns_privacy.blocklists` and `nextdns_privacy.natives` are sets, so the order the API returns them in never causes drift. Leaving one out clears it.
- `nextdns_allowlist`, `nextdns_denylist` and `nextdns_rewrite` accept no `domain` / `rewrite` blocks at all, which keeps the live list empty.
- `nextdns_parental_control.recreation` is optional. A profile without a recreation schedule (the API returns no timezone and no times) reads back as no block, and nothing is written for it. Removing a declared schedule clears it.

## Migrating from `amalucelli/nextdns`

1. Change `source` to `aeronet-wireless-broadband-llc/nextdns`, `version` to `~> 0.7`.
2. `tofu init -upgrade`
3. `tofu state replace-provider registry.opentofu.org/amalucelli/nextdns registry.opentofu.org/aeronet-wireless-broadband-llc/nextdns` (use `registry.terraform.io` on Terraform).
4. `tofu plan` should report no changes. Then author the new attributes at your leisure.

Rollback is the reverse `state replace-provider` plus reverting the source, version and mirror configuration.

## Documentation

Generated from the provider schema into [`docs/`](docs/) by `tools/gen-docs.sh` (`go generate ./...`). Full worked example in [`examples/main.tf`](examples/main.tf).

## Development

Tooling is pinned in `.mise.toml` (Go, OpenTofu, GoReleaser, golangci-lint). `tfplugindocs` is a `go.mod` tool.

```sh
mise install
go test ./...                          # stub-driven suite; TestMain points plugin-testing at the tofu on PATH
mise exec -- golangci-lint run ./...
go generate ./...                      # regenerate docs/
mise exec -- goreleaser release --snapshot --clean   # local multi-platform build into dist/
go run ./cmd/mirror-index -dist dist -version 0.3.0 -out site -copy-archives   # mirror layout, usable as a filesystem_mirror
```

Live acceptance test (creates and destroys a throwaway profile in your account; never touches an existing one):

```sh
TF_ACC=1 NEXTDNS_API_KEY=... go test ./nextdns/ -run TestAccLive -v
```

## Release

Pushing a `v*` tag builds unsigned multi-platform archives with GoReleaser, publishes them as a GitHub Release, renders the network-mirror JSON with `cmd/mirror-index` and deploys it to GitHub Pages. There is no GPG signing and no registry submission.

## Licence

MIT, as upstream (`LICENSE`: Copyright 2022 Alexandre Malucelli). The vendored client's licence is in `internal/nextdns/LICENSE-nextdns-go.md`. Changes by LukeEvansTech and AeroNet are under the same licence.
