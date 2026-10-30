# Command-line tool

open-anonymous-ip builds a MaxMind-compatible Anonymous IP database from
lists of addresses and networks.

```shell
open-anonymous-ip -tor tor.txt -hosting dc.txt -vpn vpn.txt -exclude relay.csv -o out.mmdb
```

## Flags

| Flag | What it does |
|---|---|
| `-anonymous <file>` | flag a list as `is_anonymous` only (repeatable, like every list flag) |
| `-tor <file>` | flag a list as `is_tor_exit_node` |
| `-hosting <file>` | flag a list as `is_hosting_provider` |
| `-vpn <file>` | flag a list as `is_anonymous_vpn` |
| `-public_proxy <file>` | flag a list as `is_public_proxy` |
| `-residential_proxy <file>` | flag a list as `is_residential_proxy` |
| `-exclude <file>` | remove the list's networks from the lists before it |
| `-plus` | write the Anonymous Plus layout |
| `-confidence <n>` | `anonymizer_confidence`, 1 to 99, for the lists after it, 0 to clear (needs `-plus`) |
| `-seen <date>` | `network_last_seen`, YYYY-MM-DD, for the lists after it, empty to clear (needs `-plus`) |
| `-provider <name>` | `provider_name` for the lists after it, empty to clear (needs `-plus`) |
| `-risk` | the IP Risk layout, in development, stops with an error |
| `-skip-reserved` | drop reserved networks with a count instead of stopping |
| `-type <string>` | `database_type` in the metadata |
| `-description <string>` | description in the metadata |
| `-r <size>` | record size, 24, 28, or 32, default 24 |
| `-o <file>` | output path (required) |

## Flags and order

Every flag is repeatable. An address in several lists gets every flag, and
`is_anonymous` is set whenever any other is, as in MaxMind's test files.
`-anonymous` is for a list that says "proxy" or "anonymizer" without saying
which kind (its addresses get the umbrella flag and nothing more specific).
Lists apply in command-line order, and `-exclude` clears its networks of
whatever flags came before it. A list after the exclude can flag them again, so
`-vpn vpn.txt -exclude relay.csv` drops Apple's Private Relay ranges from
`vpn`, and `-anonymous relay.csv` after that would keep them as `anonymous`
only.

## Database type and metadata

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

## Anonymous Plus

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

## List formats

A list is one address or network per line, `1.2.3.4`, `2001:db8::1`, or
`10.0.0.0/8`. Anything after a space, tab, comma, or `#` is ignored, so an
RFC 8805 geofeed reads as is. Blank lines and `#` comments are skipped,
and a line that is neither is an error naming the file and line.
`-skip-reserved` drops private and other reserved networks, which some
provider APIs list, instead of stopping on them.

A list can also be any JSON document. Every string in it that is an
address or network counts and everything else is ignored, so the cloud
providers' published feeds load as downloaded, with no schema per vendor.

## The written file

Every routable address not in any list has an empty record, as in MaxMind's
test files, so readers that treat a miss as an error do not hit one, and an
excluded address reads the same as any other ordinary address. After
writing, the tool reopens the file and checks that the first entries of
every list carry its flag, unless an exclude cleared them. IPv4 lives in
the IPv6 tree with the `::ffff:0:0/96`, `2002::/16`, and Teredo
`2001::/32` aliases MaxMind's test files carry, so IPv4-mapped lookups work.
