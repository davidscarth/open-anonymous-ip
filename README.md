# open-anonymous-ip

[![Build](https://github.com/davidscarth/open-anonymous-ip/actions/workflows/build.yml/badge.svg)](https://github.com/davidscarth/open-anonymous-ip/actions/workflows/build.yml) [![Tests](https://github.com/davidscarth/open-anonymous-ip/actions/workflows/test.yml/badge.svg)](https://github.com/davidscarth/open-anonymous-ip/actions/workflows/test.yml) [![CodeQL](https://github.com/davidscarth/open-anonymous-ip/actions/workflows/github-code-scanning/codeql/badge.svg)](https://github.com/davidscarth/open-anonymous-ip/actions/workflows/github-code-scanning/codeql)

> [!WARNING]
> Experimental. Do not use this on production or anything important.
> The sources, how they are combined, what is carved out, the flags,
> the database type string, the release layout, and the schedule may
> all change, and the project may be discontinued at any time. Use at
> your own risk.

MaxMind-compatible Anonymous IP databases built from open and public lists,
and the tool for building them. Point the tool at a Tor exit list, a datacenter
range list, a VPN range list, and it writes one `.mmdb` in the GeoIP2 Anonymous
IP record layout. Readers that work from the raw MMDB, such as
[caddy-geoip](https://github.com/davidscarth/caddy-geoip)'s `geoip_anonymous`
matcher, read it as is. For a reader that wants a MaxMind type string, see
`-type` below. Ready-made files are published every day.

## Released databases

The `build` workflow rebuilds daily and publishes two files, each with
its SHA-256, under the `latest` release. Both are built from:

| Flag | Source | License |
|---|---|---|
| `-hosting` | X4BNet [datacenter ranges](https://github.com/X4BNet/lists_vpn) | MIT |
| `-vpn` | X4BNet [VPN ranges](https://github.com/X4BNet/lists_vpn) | MIT |
| `-exclude` | Apple [Private Relay egress ranges](https://mask-api.icloud.com/egress-ip-ranges.csv) | N/A* |
| `-exclude` | Google [VPN by Google egress ranges](https://www.gstatic.com/vpn/geofeed) | N/A* |
| `-exclude` | Google's own services, [everything Google announces](https://www.gstatic.com/ipranges/goog.json) minus [Google Cloud](https://www.gstatic.com/ipranges/cloud.json) | N/A* |
| `-public_proxy` | IPLocate [free proxy list](https://github.com/iplocate/free-proxy-list), every address seen in the last 14 days, 30 in Plus | MIT |
| `-tor` | Tor Project [exit list](https://check.torproject.org/torbulkexitlist) and [Onionoo](https://onionoo.torproject.org/) | CC0 |

<sub>* *These ranges are not in the files. The lists are used only to carve
them out.*</sub>

Every source whose data enters the files is under a permissive license (MIT or
Apache-2.0) or public domain (CC0, Unlicense, or 0BSD). Reference material may
carry other open terms, such as the format spec's CC BY-SA 3.0, but it is cited
and never copied into this repository or its files. Nothing here is built from,
or checked against, any source gated behind an EULA or terms of use, paid or
free. How the files are built is described in
[docs/methodology.md](docs/methodology.md).

## Command-line tool

```shell
open-anonymous-ip -tor tor.txt -hosting datacenter.txt -vpn vpn.txt -o anonymous.mmdb
```
Each flag names the field its lists set:

| Flag | Field |
|---|---|
| `-anonymous` | `is_anonymous` only |
| `-tor` | `is_tor_exit_node` |
| `-hosting` | `is_hosting_provider` |
| `-vpn` | `is_anonymous_vpn` |
| `-public_proxy` | `is_public_proxy` |
| `-residential_proxy` | `is_residential_proxy` |

Every flag is repeatable. An address in several lists gets every flag, and
`is_anonymous` is set whenever any other is, as in MaxMind's test files.
`-anonymous` is for a list that says "proxy" or "anonymizer" without saying
which kind (its addresses get the umbrella flag and nothing more specific).
Lists apply in command-line order, and `-exclude` clears its networks of
whatever flags came before it. A list after the exclude can flag them again, so
`-vpn vpn.txt -exclude relay.csv` drops Apple's Private Relay ranges from
`vpn`, and `-anonymous relay.csv` after that would keep them as `anonymous`
only.

The database type is `Open-Anonymous-IP (compat=GeoIP2-Anonymous-IP)`, or
`Open-Anonymous-IP-Plus (compat=GeoIP-Anonymous-Plus)` with `-plus`, unless
`-type` says otherwise. The `compat=` suffix is DB-IP's convention to
assert compatibility, used because the MMDB specification reserves names
starting with `GeoIP` for MaxMind. For a reader that wants an exact MaxMind
string, you can build with `-type GeoIP2-Anonymous-IP`. The released files
keep the `compat=` types, since they are not MaxMind's data. The metadata
description names the build and the flags given, such as
`open-anonymous-ip <commit> plus - vpn, tor, hosting, public_proxy, exclude`,
unless `-description` sets it.

`-plus` writes MaxMind's Anonymous Plus layout: the six flags plus
`anonymizer_confidence`, `network_last_seen`, and `provider_name`. Those
are set by `-confidence` (1 to 99, 0 to clear), `-seen` (YYYY-MM-DD, empty
to clear), and `-provider` (empty to clear). Like `-exclude`, they apply
in command-line order, each setting the value for the lists after it, so
`-confidence 99 -seen 2026-10-29 -tor tor.txt` stamps the Tor list with
both, and a list given none carries none. An address in several lists gets
every list's flags, and the last list's extras, field by field. `-risk`,
MaxMind's IP Risk layout, is in development, and the tool stops with an
error.

A list is one address or network per line, `1.2.3.4`, `2001:db8::1`, or
`10.0.0.0/8`. Anything after a space, tab, comma, or `#` is ignored, so an
RFC 8805 geofeed reads as is. Blank lines and `#` comments are skipped,
and a line that is neither is an error naming the file and line.
`-skip-reserved` drops private and other reserved networks, which some
provider APIs list, instead of stopping on them.

A list can also be any JSON document. Every string in it that is an
address or network counts and everything else is ignored, so the cloud
providers' published feeds load as downloaded, with no schema per vendor.

Every routable address not in any list has an empty record, as in MaxMind's
test files, so readers that treat a miss as an error do not hit one, and an
excluded address reads the same as any other ordinary address. After
writing, the tool reopens the file and checks that the first entries of
every list carry its flag, unless an exclude cleared them. IPv4 lives in
the IPv6 tree with the `::ffff:0:0/96`, `2002::/16`, and Teredo
`2001::/32` aliases MaxMind's test files carry, so IPv4-mapped lookups work.

## License

Apache-2.0 for our code and documentation.

The released databases carry their sources' licenses, listed above and in
`NOTICE`. Currently MIT and CC0.

Nothing here is MaxMind's data, and this project is not affiliated with or
endorsed by MaxMind. "GeoIP" and "MaxMind" are trademarks of MaxMind, Inc.
