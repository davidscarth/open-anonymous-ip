# Methodology

It's important to detail how the released files are put together, and the
choices and rationales that go into producing them.

## Goal

A free and open source Anonymous IP database that is comparable to MaxMind's
paid Anonymous IP database, and can be used as a drop in replacement for it.

To do that, we will approximate the intent of their database using free and
open data sources, release in their database format, and use their same record
layout. For confirmed choices they make, we will do the same thing they do.
For anything we can't confirm, take a best effort approach to do what's right.
That may be a judgment call (a gut check of our sources), or data driven
(taking measurements or making comparisons), or some combination of the two.
And if we feel that MaxMind did something wrong, try and find a compatible way
to work around it, and document it here.

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

It is good data, live and frequently checked, but not usable for us as-is.
Public proxies are fickle, they may go up and down, or stay up for a little
while and never come back. So we need to keep a little history to account for
that.

Over 425 days of its snapshots (through October 29, 2026, 17,175 snapshots)
95,171 different addresses passed through it. Most of the time an address
drops off the list, it is back within the hour. 84% of all gaps are that
short, which means the checker probably missed it once, not that the proxy
died. Any single snapshot has about 1,100 addresses, but a full day of
snapshots adds up to about 4,400.

A list that only contains today's data still misses proxies that are
offline today and back tomorrow (bad recall), and a list that never forgets
will flag proxies that died months ago (bad precision). We need to pick how
long to keep these addresses in a way that balances both.

We can estimate this by asking questions against historical data. How soon do
dropped addresses come back? How likely is an address to show up again, given
how many days ago it was last seen? And if we had built the file with each
window length on every day of the past year, how well would each one have
matched what IPLocate listed the next day? That last one is scored with
F0.5, which counts being right as twice as important as being complete.

| days | how soon they come back (KM) | how likely to show up again (P(back \| gone t)) | how well a window did (F0.5) |
|-----:|-------------------:|--------------------------------------:|---------------------------:|
|    1 |                    |                                   75% |                            |
|    2 |                47% |                                   61% |                            |
|    3 |                61% |                                   51% |                            |
|    7 |                75% |                                   29% |                            |
|   14 |                81% |                                   14% |                        45% |
|   30 |                84% |                                    6% |                        33% |
|   45 |                85% |                                    3% |                        28% |
|   60 |                85% |                                    2% |                        24% |

The base file has no `anonymizer_confidence` field, so we have to be careful
when choosing a cutoff. By two weeks most of the addresses that are coming
back have come back. 81% of dropped addresses are back within two weeks,
and waiting a month only raises that to 84%. After two weeks each extra day
of history adds addresses with about a one in seven chance of being seen
again, and falling. So the base file keeps 14 days.

Plus and Risk databases have an `anonymizer_confidence` field, so in those we
can extend the tail out to 30 days and mark it low, and an end user can set
their own cutoff or use it all. How that value is set is under
`anonymizer_confidence`, below.

The replay says the same thing, the two week window beats 30 days by 11
points, with 45 and 60 further behind, and the lead holds however old the
copy is. It also says to fetch the files daily. A copy loses about a third
of its coverage in a month no matter how long its window is, because most
of what it is missing did not exist yet when it was built.

How the base and Plus files hold up as they age:

| age of database | base precision | base recall | base F0.5 | Plus precision | Plus recall | Plus F0.5 |
|----------------:|---------------:|------------:|----------:|---------------:|------------:|----------:|
|           1 day |            40% |         93% |       45% |            29% |         94% |       33% |
|          7 days |            35% |         81% |       40% |            26% |         83% |       30% |
|         14 days |            32% |         74% |       36% |            24% |         76% |       28% |
|         30 days |            28% |         62% |       32% |            21% |         65% |       24% |
|         90 days |            21% |         38% |       23% |            16% |         41% |       18% |

<sup>Plus here is the full 30 day set with no confidence cutoff applied.</sup>

Based on the data, it would be best to fetch and refresh your database daily.
Since we live in the real world, a weekly refresh is a sensible compromise,
knowing that by the end of the week the copy is missing about one in eight of
the live proxies (recall is 81% vs. 93% for a fresh copy), and a month-old db
is missing a third.

### `is_residential_proxy`

Not currently used. A residential proxy is a home connection being used as an
exit for someone else's traffic, which in practice means the commercial
residential proxy services, which route their customers' traffic out through
ordinary home connections, mostly phones and PCs running a free app that
quietly bundled the service's SDK. MaxMind's test data carries the flag on
networks with a `provider_name`, which fits that reading. No free source lists
them, and there is no network-level signal to derive them from, since the whole
point of such a service is that its exits look like anyone else. If we can find
this data at some point in a free form, it will be added.

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

How the Tor flag holds up as it ages, F0.5 by window:

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
long ago IPLocate last listed it, and on how many distinct days it was listed
in the 30 days before the build. The second matters as much as the first. An
address listed on four or more days that month is a regular and comes back far
more often than one seen once, at every age. Measured over every daily build
in the history, this is the share of addresses listed again within 14 days,
with each class's share of the row in parentheses.

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

Relisting is a floor on liveness rather than an estimate of it. IPLocate's list
is a sample of a larger population of open proxies, and an address it stops
listing has not necessarily stopped being a proxy, so the rates are how often
IPLocate saw an address again, not how often it was still there. Tor exits are
99 with the build date, since the Tor Project checked them within the hour.
Hosting and VPN ranges carry no confidence because the X4BNet ranges carry no
liveness signal to derive one from. MaxMind's test data carries the field on
VPN records, and in IP Risk on hosting records too, so we may add this in the
future.

Plus runs the proxy window to 30 days where the base file stops at 14. The base
file has to pick a cutoff, and 14 days is where the cut scores best. Plus
labels every address with how often addresses like it were listed again, so it
can carry the window out to where the signal ends instead. Addresses last seen
15 to 30 days ago come back about one time in ten, they are in the file at
confidence 10, so an end user who does not want to act on them can filter them
out.

### `network_last_seen`

Plus and Risk only. The date of the last IPLocate snapshot that listed the
address, and the build date for Tor exits. Hosting and VPN ranges carry none
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

Checked October 27, 2026.

| Reference | Networks | `anonymous` | `hosting` | `vpn` | `tor` | `public_proxy` |
|---|---:|---:|---:|---:|---:|---:|
| AWS | 1,745 | 88% | 88% | 0% | 0% | 0% |
| Google Cloud | 461 | 97% | 97% | 0% | 0% | 0% |
| Oracle Cloud | 798 | 100% | 100% | 0% | 0% | 0% |
| DigitalOcean | 184 | 100% | 100% | 0% | 0% | 0% |
| Linode | 240 | 99% | 99% | 0% | 0% | 0% |
| Cloudflare | 15 | 0% | 0% | 0% | 0% | 0% |
| Mullvad relays | 554 | 100% | 91% | 99% | 0% | 0% |
| NordVPN servers | 7,035 | 99% | 99% | 62% | 0% | 0% |
| Private Internet Access servers | 1,522 | 95% | 90% | 50% | 0% | 0% |
| AirVPN servers | 690 | 91% | 91% | 30% | 0% | 0% |
| IVPN servers | 177 | 91% | 91% | 25% | 0% | 0% |
| iCloud Private Relay egress | 3,490 | 0% | 0% | 0% | 0% | 0% |
| IPLocate proxies, current list | 1,093 | 100% | 43% | 0% | 0% | 100% |
| Tor Project, Onionoo exits | 652 | 100% | 44% | 22% | 99% | 0% |
| dan.me.uk Tor exits | 669 | 98% | 49% | 23% | 94% | 0% |

Checked October 28, 2026, with MISP warninglists as the references.

| Reference | Networks | `anonymous` | `hosting` | `vpn` | `tor` | `public_proxy` |
|---|---:|---:|---:|---:|---:|---:|
| Azure | 1,093 | 100% | 100% | 0% | 0% | 0% |
| OVH shared-hosting clusters | 276 | 100% | 100% | 0% | 0% | 0% |
| Office 365 | 31 | 100% | 100% | 0% | 0% | 0% |
| Googlebot | 37 | 100% | 100% | 0% | 0% | 0% |
| Gmail sending ranges | 2 | 100% | 100% | 0% | 0% | 0% |
| Akamai | 285 | 95% | 95% | 0% | 0% | 0% |
| Public DNS resolvers | 61,025 | 9% | 9% | 0% | 0% | 0% |
| Wikimedia | 7 | 0% | 0% | 0% | 0% | 0% |
| X4BNet Tor exits | 831 | 93% | 47% | 19% | 82% | 0% |

iCloud Private Relay reads zero because it is excluded.

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
  MIME Type for CSV Files. Apple's Private Relay feed and IP2Proxy's
  CSV.
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
- Apple, [Prepare your network for iCloud Private Relay](https://developer.apple.com/support/prepare-your-network-for-icloud-private-relay/).
- Google, [IP address ranges](https://support.google.com/a/answer/10026322)
  for `goog.json` and `cloud.json`, and
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
