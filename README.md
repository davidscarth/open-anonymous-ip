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
matcher, read it as is. Ready-made files are published every day.

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

Lists apply in command-line order, and `-exclude` carves networks out of
what came before it. A list is one address or network per line, a geofeed,
or any JSON document with addresses in it. `-plus` adds the Anonymous Plus
fields. The full reference, every flag, format, and the type strings, is
in [docs/tool.md](docs/tool.md).

## License

Apache-2.0 for our code and documentation.

The released databases carry their sources' licenses, listed above and in
`NOTICE`. Currently MIT and CC0.

This project is not affiliated with or endorsed by MaxMind. "GeoIP" and
"MaxMind" are trademarks of MaxMind, Inc.
