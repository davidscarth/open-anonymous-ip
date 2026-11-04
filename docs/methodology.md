# Methodology

It's important to detail how the released files are put together, and the
choices and rationales that go into producing them.

## Goal

A free and open source Anonymous IP database that is comparable to MaxMind's
paid Anonymous IP database, and can be used as a drop in replacement for it.

To do that, we will approximate the intent of their database using free and
open data sources, release in their database format, and use their same record
layout. For choices they make that we can confirm from the format specification
or their open test data, we will do the same thing they do. For anything we
can't confirm, take a best effort approach to do what's right. That may be a
judgment call (a gut check of our sources), or data driven (taking measurements
or making comparisons), or some combination of the two. And if we feel that
MaxMind did something wrong, try and find a compatible way to work around it,
and document it here.

## The files

### Format
We will release in the same [MaxMind DB File Format][mmdb-spec] (.mmdb), which
is a format published as open source (CC BY-SA 3.0). They are also kind enough
to publish open source reader libraries for most languages, writer libraries
for Go and Perl, and a couple of command line tools.

The mmdb format does not define any particular schema, that is left to each
author. For our purposes, we seek to be compatible with the main Anonymous
databases that MaxMind makes.

### Schema

MaxMind publishes test databases for each layout in the MaxMind-DB repository,
dual-licensed Apache-2.0 or MIT. We used a small tool
(docs/artifacts/schema.go) to walk the test files and map the schema, over the
2026-09-02 test databases (MaxMind-DB at commit `363086b`, every GeoIP and
GeoLite2 file except the two corrupt fixtures, tool output saved in
docs/artifacts/schema.txt).

| Field | Type | Anonymous IP | Anonymous Plus | IP Risk |
|---|---|---|---|---|
| `is_anonymous` | bool, present only when true | yes | yes | yes |
| `is_anonymous_vpn` | bool, present only when true | yes | yes | yes |
| `is_hosting_provider` | bool, present only when true | yes | yes | yes |
| `is_public_proxy` | bool, present only when true | yes | yes | yes |
| `is_residential_proxy` | bool, present only when true | yes | yes | yes |
| `is_tor_exit_node` | bool, present only when true | yes | yes | yes |
| `anonymizer_confidence` | uint16, 1 to 99 observed | | yes | yes |
| `network_last_seen` | string, YYYY-MM-DD observed | | yes | yes |
| `provider_name` | string | | yes | yes |
| `ip_risk` | double, 25 to 99 observed | | | yes |

We will use the same schema as listed in the table.

Their test data uses `anonymizer_confidence` values of 1 through 99 (and values
of 30 through 99 in the three file types of interest). `provider_name` exists
on VPN, public proxy, and residential proxy records. The Anonymous IP and
Anonymous Plus test files also carry an empty record for every public address
that is not an anonymizer, so a lookup of any routable address finds a record
and a non-anonymous address decodes to all flags false. Reserved networks,
private space included, are not in the tree and look up as not found. IP Risk
does not carry the empty record.

#### Type

The type strings reported by the test files are `GeoIP2-Anonymous-IP`,
`GeoIP-Anonymous-Plus`, and `GeoIP2-IP-Risk`. Our files use the same record
layout, but the MMDB specification reserves names starting with `GeoIP` for
MaxMind. DB-IP's convention to assert compatibility with MaxMind is the
`compat=` suffix.

We will use the strings `Open-Anonymous-IP (compat=GeoIP2-Anonymous-IP)` and
`Open-Anonymous-IP-Plus (compat=GeoIP-Anonymous-Plus)` in the same fashion
as DB-IP does to assert compatibility without claiming to be from MaxMind.

## Data sources, by field

### `is_anonymous`

The umbrella flag. This flag may be set alone, or if any other flag is set,
then this one is set as well. The tool also takes `-anonymous` for a list that
says "proxy" or "anonymizer" without saying which kind, which sets this flag
alone. This allows any generic anonymizer list to enrich the data. For the
database we release, all of our data sources are more specific, so every entry
will carry this and at least one other flag.

### `is_anonymous_vpn`
X4BNet's [VPN list][x4b-vpn] (MIT licensed), built from keyword matching on ASN
records plus a curated [list of VPN provider ASNs][x4b-vpn-asn] that announce
their own networks, refreshed daily from BGP.

### `is_hosting_provider`
X4BNet's [datacenter list][x4b-dc] (MIT licensed), built the same way from a
maintained [list of hosting and cloud providers][x4b-dc-asn]. Hosting is a
property of the network, so ASN is the right key, and the coverage tables below
show the clouds that publish their own ranges at 88 to 100%.

Exclusions:
Two providers publish what their non-customer ranges are, and we remove exactly
what they declare. Apple publishes Private Relay's
[egress ranges][apple-relay]. Google defines its own services as everything in
[`goog.json`][goog] that is not in [`cloud.json`][gcloud], and the build
computes that difference. Google also publishes the egress ranges of VPN by
Google, the relay built into Pixel phones [as a geofeed][goog-vpn], and it is
removed the same way as Private Relay. Akamai declares nothing, and its
BGP-derived lists cannot be separated from Linode, the cloud it bought, so
Akamai's CDN space stays however the X4BNet list classifies it rather than
carve out rentable servers with it. Nothing from any feed is inserted into the
files, only removed.

### `is_public_proxy`

IPLocate's [free proxy list][iplocate] (MIT licensed) is small and live, one
to two thousand addresses that answered in the last half hour, committed to
[git][iplocate-git] about every half hour.

stormsia's [proxy list][stormsia] (MIT licensed) is a few hundred working
proxies checked every 15 minutes and committed to git as
[`proxies.json`][stormsia-json]. For each proxy it records the address and
the `exit_ip`, the address the checker's request came from after passing
through the proxy. In 86% of the proxy and exit pairs over five months of
snapshots (May 29 to November 3, 2026) the two differ: the proxy forwards
to another machine, and the exit is what a website sees. 49,319 proxy
addresses led to 111,371 exit addresses, and 76,753 of the exits never
appeared as a proxy in either list. The proxy addresses overlap IPLocate's
by 72% over a 14-day window. The exits are what stormsia adds. The build
reads only the address, the exit, and the commit time from `proxies.json`.

It is good data, live and frequently checked, but not usable for us as-is.
Public proxies are fickle, they may go up and down, or stay up for a little
while and never come back. So we need to keep a little history to account for
that.

Over 425 days of IPLocate's snapshots (through October 29, 2026, 17,175
snapshots) 95,171 different addresses passed through it. Most of the time
an address drops off the list, it is back within the hour. 84% of all gaps
are that short, which means the checker probably missed it once, not that
the proxy died. Any single snapshot has about 1,100 addresses, but a full
day of snapshots adds up to about 4,400. A day of stormsia's snapshots adds
up to about 7,200 addresses.

A list that only contains today's data misses proxies that are offline today
and back tomorrow, and a list that never forgets flags proxies that died
months ago. Measured over the history, most addresses that come back do so
quickly. Of the addresses that drop off for more than 24 hours, the share
that returned by day N:

| returned by day |  2 |  3 |  7 | 14 | 30 | 60 |
|---:|---:|---:|---:|---:|---:|---:|
| IPLocate | 47% | 61% | 75% | 81% | 84% | 85% |
| stormsia | 37% | 49% | 63% | 70% | 73% | 75% |

The curve is steep through the first week, levels off by two weeks, and is
flat after. 81% of IPLocate's dropped addresses are back within 14 days, and
waiting another two weeks raises that to only 84%. An address last seen 7
days ago still has a 29% chance of being listed again, so cutting at one
week is too early. By day 14 that chance has fallen to 14%, and by day 30 to
6%. stormsia's curve runs about ten points lower and flattens at the same
point.

The base file lacks a confidence field, so every address is either flagged
as a proxy or not. Without a way to gauge confidence, we pick a cutoff where
the curve flattens out (14 days).

Plus and Risk databases have the `anonymizer_confidence` field, so in those we
can extend the tail out to 30 days and mark it low, and an end user can set
their own cutoff or use the whole range. How that value is set is under
`anonymizer_confidence`, below.

The two lists are folded into one history per address before the windows
are applied. An address's last listing and its days listed count every
sighting, as a proxy in either list or as an exit in stormsia's, so an
address in both lists gets one date and one confidence.

How the base and Plus files hold up as they age, measured on each list
alone:

| age of database | base precision | base recall | Plus precision | Plus recall |
|----------------:|---------------:|------------:|---------------:|------------:|
| IPLocate, 1 day |            40% |         93% |            29% |         94% |
|          7 days |            35% |         81% |            26% |         83% |
|         14 days |            32% |         74% |            24% |         76% |
|         30 days |            28% |         62% |            21% |         65% |
|         90 days |            21% |         38% |            16% |         41% |
| stormsia, 1 day |            35% |         91% |            25% |         92% |
|          7 days |            31% |         81% |            22% |         82% |
|         14 days |            28% |         73% |            20% |         75% |
|         30 days |            24% |         62% |            16% |         64% |
|         90 days |            14% |         40% |            10% |         42% |

<sup>Plus here is the full 30 day set with no confidence cutoff applied.</sup>

Based on the data, it would be best to fetch and refresh your database daily.
Since we live in the real world, a weekly refresh is a sensible compromise,
knowing that by the end of the week the copy is missing about one in eight of
the live proxies (recall is 81% vs. 93% for a fresh copy), and a month-old db
is missing a third.

### `is_residential_proxy`

A residential proxy, for this flag, is a proxy address on a consumer ISP's
network: broadband, DSL, cable, fiber, or mobile. The proxy lists above say
which addresses are proxies. For the network, [ipverse][ipverse] publishes
under CC0 a category for every ASN ([as-metadata][ipverse-meta]: `isp`,
`hosting`, `business`, `education_research`, or `government_admin`) and
the prefixes every ASN announces ([as-ip-blocks][ipverse-blocks]),
refreshed daily from the registries and BGP route collectors.

An address from the public proxy lists, proxy or exit, carries this flag
when it is in a prefix announced by an ASN ipverse classifies as `isp` and
not in a hosting range from the X4BNet list. The categories are read from
`as-metadata`; the copy of the category in each `as-ip-blocks` file lags
it. Nothing from ipverse is inserted into the files.

The two classifications agree where they overlap. Over stormsia's five
months of exits, 94% of the addresses in X4BNet's hosting ranges are on
ASNs ipverse calls `hosting`. Of the 79,598 exits outside those ranges,
ipverse puts 72% on ISPs, 11% on hosting providers X4BNet does not list,
and 13% on business networks. The last two are why the flag needs a
positive ISP test and not just "not hosting".

Measured the same way as the public proxies, on the exits on ISP networks
(57,686 addresses over the 159 days, about 4,200 a day), the share of
dropped addresses that returned by day N:

| returned by day |  2 |  3 |  7 | 14 | 30 | 60 |
|---:|---:|---:|---:|---:|---:|---:|
| exits on ISP networks | 40% | 52% | 66% | 72% | 75% | 77% |

and the files as they age:

| age of database | base precision | base recall | Plus precision | Plus recall |
|----------------:|---------------:|------------:|---------------:|------------:|
|           1 day |            40% |         92% |            29% |         93% |
|          7 days |            36% |         84% |            26% |         84% |
|         14 days |            33% |         77% |            24% |         78% |
|         30 days |            29% |         65% |            21% |         66% |
|         90 days |            18% |         41% |            14% |         43% |

The curve flattens at 14 days and the aging matches IPLocate's within a
point or two, so the residential lists use the same windows, 14 days in
the base file and 30 in Plus, and the same confidence table. Every address
with this flag also carries `is_public_proxy`, and in Plus the same
`network_last_seen` and `anonymizer_confidence`.

### `is_tor_exit_node`

The [bulk exit list][torbulk] (CC0, public domain) is the IPv4 addresses the
Tor Project itself observed traffic leaving the network from. Nobody measures
IPv6 egress, so for IPv6 we take the addresses of the exit relays from
[Onionoo][onionoo] (CC0, public domain), the Tor Project's relay directory
service, filtered to relays with the Exit flag.

Neither list gets a window (like we had to do with the public proxies), and
that was measured rather than assumed. Tor exits come from the network's own
directory, so an address is listed or it is not, and six months of the Tor
Project's hourly [exit lists][collector] (May through October 2026, 4,181
snapshots, 2,893 addresses) bear that out. The median exit stays listed for 32
days, and none of the 1,168 absences was shorter than an hour. Exits that drop
mostly stay gone, 12% are back within a day and 40% within a month. Holding
past sightings adds no recall at any age of copy and only keeps dead exits. So
the flag is the current lists as their publishers serve them. The Tor flag
ages well, a week-old copy is still 95% right and a month-old one 89%.

How the Tor flag holds up as it ages, precision by window:

| age of database | current list | 7-day window | 14-day window |
|----------------:|-------------:|-------------:|--------------:|
|          1 hour |         100% |          95% |           91% |
|           1 day |          99% |          94% |           91% |
|          7 days |          95% |          91% |           88% |
|         30 days |          89% |          86% |           83% |

### `anonymizer_confidence`

Plus and Risk only. The test data shows a range of 1 to 99. We emit five
values: 99, 70, 50, 30, and 10 (steps that map roughly onto the measured decay
curve below). The longer since an address was last listed, and the fewer days
it was listed, the lower the confidence.

The value is set by two things the build already knows about an address, how
long ago a proxy list last listed it, and on how many distinct days it was
listed in the 30 days before the build. The second matters as much as the
first. An address listed on four or more days that month is a regular and
comes back far more often than one seen once, at every age. Measured over
every daily build in IPLocate's history, this is the share of addresses
listed again within 14 days, with each class's share of the row in
parentheses.

| last listed | listed 1 day | 2-3 days  | 4+ days   |
|------------:|-------------:|----------:|----------:|
|   under 24h |     67% (6%) | 79% (8%)  | 97% (87%) |
|      24-48h |    33% (12%) | 53% (14%) | 86% (75%) |
|     48-120h |    22% (19%) | 38% (20%) | 68% (60%) |
| 120h-8 days |    15% (26%) | 28% (25%) | 45% (48%) |
|   8-14 days |    11% (31%) | 21% (28%) | 26% (41%) |
|  15-30 days |     8% (43%) | 10% (31%) | 10% (26%) |

Counting the history over 60 days instead of 30 moves no cell to a different
value, and counting over 14 days cannot see the second half of the window, so
30 it is. The assigned value is the nearest of 99, 70, 50, 30, and 10 to the
measured rate, except that 99 is kept for addresses listed in the last 24
hours, meaning the source checked them within the day.

| last listed | listed 1 day | 2-3 days | 4+ days |
|------------:|-------------:|---------:|--------:|
|   under 24h |           70 |       70 |      99 |
|      24-48h |           30 |       50 |      70 |
|     48-120h |           30 |       30 |      70 |
| 120h-8 days |           10 |       30 |      50 |
|   8-14 days |           10 |       30 |      30 |
|  15-30 days |           10 |       10 |      10 |

The relisting rates are a floor on liveness. They just say how often the
list saw an address again, not how often it was still there, so a value of
10 means "rarely relisted", not "90% dead".

The same measurement over stormsia's exits on ISP networks, beside
IPLocate's:

| last listed | listed 1 day | | 2-3 days | | 4+ days | |
|---:|---:|---:|---:|---:|---:|---:|
| | IPLocate | stormsia | IPLocate | stormsia | IPLocate | stormsia |
|   under 24h | 67% | 46% | 79% | 72% | 97% | 97% |
|      24-48h | 33% | 17% | 53% | 38% | 86% | 84% |
|     48-120h | 22% | 10% | 38% | 28% | 68% | 70% |
| 120h-8 days | 15% |  7% | 28% | 20% | 45% | 48% |
|   8-14 days | 11% |  5% | 21% | 13% | 26% | 26% |
|  15-30 days |  8% |  4% | 10% |  8% | 10% | 11% |

The 4+ days column, which holds most of the addresses in every row under
15 days, agrees within three points. The other two columns run lower for
stormsia, and four cells would map one step down. The two histories are
folded together before the value is set, so one table is used, the one
above from IPLocate.

Plus runs the proxy window to 30 days where the base file stops at 14, as
described under `is_public_proxy` above. The confidence field is what makes the
longer window safe: addresses last seen 15 to 30 days ago come back about one
time in ten, they are in the file at confidence 10, so an end user who does not
want to act on them can filter them out.

### `network_last_seen`

Plus and Risk only. The date of the last IPLocate or stormsia snapshot that
listed the address, and the build date for Tor exits. Hosting and VPN ranges carry none
in the source data, so we can't include it.

### `provider_name`

Plus and Risk only. Supported by the tool, but not set yet in releases.

### `ip_risk`

Risk only. Not released, and `-risk` is in development, the tool stops with
an error. The field types are known from MaxMind's test data, but we have no
score to put in it yet.

MaxMind's is a probability from their fraud network. Anything of ours would be
a reputation score from public blocklists, a different meaning in the same
field, and it needs its own sources and scoring, which are not yet built.

## Coverage

Each reference list below is built into its own database with this tool and
checked, network by network, against that day's base file. A reference
network counts toward a flag in proportion to the share of its addresses that
carry it. The references are checked against, not built from, and none of
them is in the released files.

Checked November 3, 2026, with each provider's published feed as the reference.

| Reference | Networks | `anonymous` | `hosting` | `vpn` | `tor` | `public_proxy` | `residential_proxy` |
|---|---:|---:|---:|---:|---:|---:|---:|
| AWS | 11,462 | 81% | 81% | 0% | 0% | 0% | 0% |
| Google Cloud | 1,151 | 100% | 100% | 0% | 0% | 0% | 0% |
| Oracle Cloud | 1,342 | 98% | 98% | 0% | 0% | 0% | 0% |
| DigitalOcean | 1,238 | 100% | 100% | 0% | 0% | 0% | 0% |
| Linode | 5,505 | 99% | 99% | 0% | 0% | 0% | 0% |
| Cloudflare | 15 | 0% | 0% | 0% | 0% | 0% | 0% |
| Mullvad relays | 1,135 | 95% | 91% | 62% | 0% | 0% | 0% |
| NordVPN servers | 7,173 | 99% | 99% | 62% | 0% | 0% | 0% |
| Private Internet Access servers | 1,662 | 96% | 91% | 53% | 0% | 0% | 0% |
| AirVPN servers | 2,000 | 91% | 91% | 30% | 0% | 0% | 0% |
| IVPN servers | 192 | 89% | 89% | 24% | 0% | 0% | 0% |
| iCloud Private Relay egress | 285,129 | 0% | 0% | 0% | 0% | 0% | 0% |
| VPN by Google egress | 14,434 | 0% | 0% | 0% | 0% | 0% | 0% |
| IPLocate proxies, current list | 1,436 | 100% | 36% | 0% | 0% | 100% | 49% |
| ProxyScrape proxies, current list | 1,565 | 99% | 20% | 0% | 0% | 99% | 58% |
| Tor Project, Onionoo exits | 2,296 | 98% | 47% | 23% | 96% | 27% | 6% |
| dan.me.uk Tor exits | 2,089 | 99% | 51% | 25% | 96% | 26% | 6% |

Checked November 3, 2026, with MISP warninglists as the references.

| Reference | Networks | `anonymous` | `hosting` | `vpn` | `tor` | `public_proxy` | `residential_proxy` |
|---|---:|---:|---:|---:|---:|---:|---:|
| Azure | 2,337 | 98% | 98% | 0% | 0% | 0% | 0% |
| OVH shared-hosting clusters | 622 | 100% | 100% | 0% | 0% | 0% | 0% |
| Office 365 | 82 | 100% | 100% | 0% | 0% | 0% | 0% |
| Googlebot | 59 | 39% | 39% | 0% | 0% | 0% | 0% |
| Gmail sending ranges | 8 | 0% | 0% | 0% | 0% | 0% | 0% |
| Akamai | 346 | 40% | 40% | 0% | 0% | 0% | 0% |
| Public DNS resolvers | 62,780 | 10% | 10% | 0% | 0% | 0% | 0% |
| Wikimedia | 10 | 0% | 0% | 0% | 0% | 0% | 0% |
| X4BNet Tor exits | 831 | 91% | 54% | 4% | 70% | 27% | 6% |

iCloud Private Relay, VPN by Google, and Gmail read zero because they are
excluded. Googlebot reads 39% because part of its published ranges is
outside the Google-own carve-out.

## References

Addresses and networks

- [RFC 4632](https://www.rfc-editor.org/rfc/rfc4632), Classless
  Inter-domain Routing (CIDR). The `a.b.c.d/n` form every list uses.
- [RFC 4291](https://www.rfc-editor.org/rfc/rfc4291), IP Version 6
  Addressing Architecture. IPv4-mapped addresses, `::ffff:0:0/96`, the
  form the MMDB tree stores IPv4 in.
- [RFC 5952](https://www.rfc-editor.org/rfc/rfc5952), A Recommendation
  for IPv6 Address Text Representation. The canonical lowercase,
  compressed form the tool normalizes input to.
- [RFC 4007](https://www.rfc-editor.org/rfc/rfc4007), IPv6 Scoped
  Address Architecture. Zone identifiers (`fe80::1%eth0`), which the
  tool refuses.
- [RFC 3056](https://www.rfc-editor.org/rfc/rfc3056), Connection of IPv6
  Domains via IPv4 Clouds (6to4). `2002::/16`, one of the three IPv4 aliases
  MaxMind's files carry, with `::ffff:0:0/96` and Teredo's `2001::/32`.
- [RFC 6890](https://www.rfc-editor.org/rfc/rfc6890), Special-Purpose
  IP Address Registries, with the IANA
  [IPv4](https://www.iana.org/assignments/iana-ipv4-special-registry)
  and
  [IPv6](https://www.iana.org/assignments/iana-ipv6-special-registry)
  registries. The reserved networks `-skip-reserved` drops, including
  [RFC 1918](https://www.rfc-editor.org/rfc/rfc1918) private space,
  [RFC 6598](https://www.rfc-editor.org/rfc/rfc6598) shared address
  space, and the [RFC 5737](https://www.rfc-editor.org/rfc/rfc5737) and
  [RFC 3849](https://www.rfc-editor.org/rfc/rfc3849) documentation
  prefixes.

List formats

- [RFC 8805](https://www.rfc-editor.org/rfc/rfc8805), A Format for
  Self-Published IP Geolocation Feeds. The geofeed CSV the tool reads
  as a list, used by DigitalOcean, Linode, and VPN by Google.
- [RFC 9632](https://www.rfc-editor.org/rfc/rfc9632), Finding and Using
  Geofeed Data. The signature trailer some geofeeds carry, which the
  tool skips as a comment.
- [RFC 8259](https://www.rfc-editor.org/rfc/rfc8259), The JavaScript
  Object Notation (JSON) Data Interchange Format. The cloud providers'
  range feeds and MISP warninglists.
- [RFC 4180](https://www.rfc-editor.org/rfc/rfc4180), Common Format and
  MIME Type for CSV Files (e.g. Apple's Private Relay feed).
- [RFC 3629](https://www.rfc-editor.org/rfc/rfc3629), UTF-8. The
  encoding lists must be in, byte order mark tolerated; UTF-16 and
  invalid UTF-8 are errors.
- [ISO 3166-1](https://www.iso.org/iso-3166-country-codes.html) and
  [ISO 3166-2](https://www.iso.org/iso-3166-country-codes.html), country
  and subdivision codes, as used in geofeed columns.

Database format and schema

- [MaxMind DB File Format Specification](https://maxmind.github.io/MaxMind-DB/),
  version 2.0. Record sizes 24, 28, and 32, the `GeoIP` name
  reservation, metadata fields.
- [MaxMind-DB test data](https://github.com/maxmind/MaxMind-DB),
  Apache-2.0 or MIT. The schema enumeration in Schema above.
- [RFC 3339](https://www.rfc-editor.org/rfc/rfc3339), Date and Time on
  the Internet. `network_last_seen` is its full-date form, `YYYY-MM-DD`,
  a profile of ISO 8601; `build_epoch` is Unix time.
- [FIPS 180-4](https://csrc.nist.gov/pubs/fips/180-4/upd1/final),
  Secure Hash Standard. The SHA-256 published beside each file.

Sources

- Tor Project [exit list](https://check.torproject.org/torbulkexitlist),
  [Onionoo protocol](https://metrics.torproject.org/onionoo.html), and
  [CollecTor exit list archive](https://collector.torproject.org/archive/exit-lists/).
- X4BNet [lists_vpn](https://github.com/X4BNet/lists_vpn).
- IPLocate [free-proxy-list](https://github.com/iplocate/free-proxy-list).
- stormsia [proxy-list](https://github.com/stormsia/proxy-list).
- ipverse [as-metadata](https://github.com/ipverse/as-metadata) and
  [as-ip-blocks](https://github.com/ipverse/as-ip-blocks), CC0.
- Apple, [Prepare your network for iCloud Private Relay](https://developer.apple.com/support/prepare-your-network-for-icloud-private-relay/).
- Google, [IP address ranges](https://support.google.com/a/answer/10026322)
  for `goog.json` and `cloud.json`,
  [VPN by Google egress ranges](https://www.gstatic.com/vpn/geofeed), and
  [Googlebot ranges](https://developers.google.com/crawling/docs/verifying-googlebot).
- MISP [warninglists](https://github.com/MISP/misp-warninglists), CC0.

[mmdb-spec]: https://maxmind.github.io/MaxMind-DB/
[torbulk]: https://check.torproject.org/torbulkexitlist
[onionoo]: https://metrics.torproject.org/onionoo.html
[collector]: https://collector.torproject.org/archive/exit-lists/
[x4b-dc]: https://github.com/X4BNet/lists_vpn/tree/main/output/datacenter
[x4b-dc-asn]: https://github.com/X4BNet/lists_vpn/blob/main/input/datacenter/ASN.txt
[x4b-vpn]: https://github.com/X4BNet/lists_vpn/tree/main/output/vpn
[x4b-vpn-asn]: https://github.com/X4BNet/lists_vpn/blob/main/input/vpn/ASN.txt
[apple-relay]: https://mask-api.icloud.com/egress-ip-ranges.csv
[goog]: https://www.gstatic.com/ipranges/goog.json
[goog-vpn]: https://www.gstatic.com/vpn/geofeed
[gcloud]: https://www.gstatic.com/ipranges/cloud.json
[iplocate]: https://github.com/iplocate/free-proxy-list
[iplocate-git]: https://github.com/iplocate/free-proxy-list/commits/main/all-proxies.txt
[stormsia]: https://github.com/stormsia/proxy-list
[stormsia-json]: https://github.com/stormsia/proxy-list/commits/main/web/public/proxies.json
[ipverse]: https://github.com/ipverse
[ipverse-meta]: https://github.com/ipverse/as-metadata
[ipverse-blocks]: https://github.com/ipverse/as-ip-blocks
