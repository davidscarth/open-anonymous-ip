// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maxmind/mmdbwriter/v2"
	"github.com/maxmind/mmdbwriter/v2/inserter"
	"github.com/maxmind/mmdbwriter/v2/mmdbtype"
	"github.com/oschwald/maxminddb-golang/v2"
)

// TestBuild writes three small lists, builds a raw tree the way run
// assembles one, and reads it back: each flag lands on its list, an
// address in two lists carries both, and is_anonymous is always set. The
// tree here carries no empty default record, that is run's doing and
// TestRun covers it, so an address in no list has no record.
func TestBuild(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	tor := write("tor.txt", "\ufeff# exits\n185.220.101.1\n185.220.101.2 # also\n2a0b:f4c2::1\n")
	hosting := write("dc.txt", "185.220.101.0/24\n1.12.0.0/14\n\n")
	vpn := write("vpn.txt", "1.12.0.0/14\t# shared with hosting\n")
	anon := write("anon.txt", "5.5.5.0/24\n")

	tree, err := mmdbwriter.New(mmdbwriter.Options{
		DatabaseType: "Open-Anonymous-IP (compat=GeoIP2-Anonymous-IP)",
		IPVersion:    6,
		RecordSize:   24,
	})
	if err != nil {
		t.Fatal(err)
	}
	merge := func(field string) func(netip.Prefix) error {
		rec, err := record(field, extras{}, modeBase)
		if err != nil {
			t.Fatal(err)
		}
		return func(p netip.Prefix) error { return tree.InsertPureFunc(p, rec, inserter.TopLevelMerge) }
	}
	remove := func(p netip.Prefix) error { return tree.InsertPureFunc(p, mmdbtype.Map{}, inserter.Remove) }
	for _, in := range []struct{ path, field string }{
		{tor, "is_tor_exit_node"},
		{hosting, "is_hosting_provider"},
		{vpn, "is_anonymous_vpn"},
		{anon, "is_anonymous"},
	} {
		n, _, err := load(in.path, merge(in.field))
		if err != nil {
			t.Fatalf("%s: %v", in.path, err)
		}
		if n == 0 {
			t.Fatalf("%s: nothing loaded", in.path)
		}
	}
	// An exclude carves its networks out of whatever was loaded before it,
	// a later list can add them back under another flag.
	if _, _, err := load(write("ex.txt", "185.220.101.2\n1.13.0.0/16\n"), remove); err != nil {
		t.Fatal(err)
	}
	if _, _, err := load(write("readd.txt", "1.13.0.0/16\n"), merge("is_anonymous")); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.mmdb")
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tree.WriteTo(f); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := maxminddb.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if db.Metadata.DatabaseType != "Open-Anonymous-IP (compat=GeoIP2-Anonymous-IP)" {
		t.Errorf("type %q", db.Metadata.DatabaseType)
	}
	for _, tc := range []struct {
		addr string
		want map[string]bool
	}{
		{"185.220.101.1", map[string]bool{"is_anonymous": true, "is_tor_exit_node": true, "is_hosting_provider": true}},
		{"185.220.101.200", map[string]bool{"is_anonymous": true, "is_hosting_provider": true, "is_tor_exit_node": false}},
		{"1.13.0.1", map[string]bool{"is_anonymous": true, "is_hosting_provider": false, "is_anonymous_vpn": false}},
		{"1.14.0.1", map[string]bool{"is_anonymous": true, "is_hosting_provider": true, "is_anonymous_vpn": true}},
		{"2a0b:f4c2::1", map[string]bool{"is_anonymous": true, "is_tor_exit_node": true}},
		{"5.5.5.9", map[string]bool{"is_anonymous": true, "is_tor_exit_node": false, "is_anonymous_vpn": false}},
	} {
		var got map[string]bool
		res := db.Lookup(netip.MustParseAddr(tc.addr))
		if err := res.Decode(&got); err != nil {
			t.Fatalf("%s: %v", tc.addr, err)
		}
		if !res.Found() {
			t.Errorf("%s: no record", tc.addr)
			continue
		}
		for k, v := range tc.want {
			if got[k] != v {
				t.Errorf("%s: %s = %v, want %v (record %v)", tc.addr, k, got[k], v, got)
			}
		}
	}
	if db.Lookup(netip.MustParseAddr("8.8.8.8")).Found() {
		t.Error("8.8.8.8: expected no record")
	}
	if db.Lookup(netip.MustParseAddr("185.220.101.2")).Found() {
		t.Error("185.220.101.2: expected no record after exclude")
	}
	// A lookup by the IPv4-mapped IPv6 form finds the same record.
	var mapped map[string]bool
	if err := db.Lookup(netip.MustParseAddr("::ffff:185.220.101.1")).Decode(&mapped); err != nil || !mapped["is_tor_exit_node"] {
		t.Errorf("::ffff:185.220.101.1: got %v, %v", mapped, err)
	}

	// Published feeds, each in its own shape, cut down to a few entries.
	// Every string that is an address or network counts, nothing else does.
	feeds := map[string]string{
		"aws.json":        `{"syncToken":"1761600000","prefixes":[{"ip_prefix":"3.5.140.0/22","region":"ap-northeast-2","service":"AMAZON"}],"ipv6_prefixes":[{"ipv6_prefix":"2600:1f00::/40"}]}`,
		"google.json":     `{"creationTime":"2026-10-27T00:00:00","prefixes":[{"ipv4Prefix":"34.80.0.0/15","service":"Google Cloud","scope":"asia-east1"},{"ipv6Prefix":"2600:1900::/35"}]}`,
		"azure.json":      `{"changeNumber":300,"cloud":"Public","values":[{"name":"ActionGroup","id":"ActionGroup","properties":{"changeNumber":40,"region":"","systemService":"ActionGroup","addressPrefixes":["13.66.60.119/32","2603:1000:4::/64"]}}]}`,
		"oracle.json":     `{"last_updated_timestamp":"2026-10-27T00:00:00.000000","regions":[{"region":"us-phoenix-1","cidrs":[{"cidr":"129.146.0.0/21","tags":["OCI"]}]}]}`,
		"cloudflare.json": `{"result":{"ipv4_cidrs":["173.245.48.0/20"],"ipv6_cidrs":["2400:cb00::/32"],"etag":"38f79d050aa027e3be3865e495dcc9bc"},"success":true,"errors":[],"messages":[]}`,
		"fastly.json":     `{"addresses":["23.235.32.0/20"],"ipv6_addresses":["2a04:4e40::/32"]}`,
		"github.json":     `{"verifiable_password_authentication":false,"hooks":["192.30.252.0/22"],"web":["140.82.112.0/20","2a0a:a440::/29"]}`,
		"geofeed.csv":     "# RFC 8805\n5.6.7.0/24,US,US-NJ,Newark,\n5.6.8.0/24,US,US-NJ,North Bergen,07047\n",
		"bom.json":        "\ufeff[\"9.9.9.9\"]",
	}
	want := map[string]int{"aws.json": 2, "google.json": 2, "azure.json": 2, "oracle.json": 1, "cloudflare.json": 2, "fastly.json": 2, "github.json": 3, "geofeed.csv": 2, "bom.json": 1}
	for name, body := range feeds {
		n, _, err := load(write(name, body), merge("is_hosting_provider"))
		if err != nil {
			t.Errorf("%s: %v", name, err)
		} else if n != want[name] {
			t.Errorf("%s: loaded %d networks, want %d", name, n, want[name])
		}
	}

	// Bad input is an error naming the line, not a smaller database.
	bad := write("bad.txt", "1.2.3.4\nnot-an-address\n")
	if _, _, err := load(bad, merge("is_tor_exit_node")); err == nil {
		t.Error("expected an error for a malformed line")
	}
	empty := write("empty.txt", "# nothing\n")
	if _, _, err := load(empty, merge("is_tor_exit_node")); err == nil {
		t.Error("expected an error for an empty list")
	}
	noaddrs := write("noaddrs.json", `{"ok":true,"items":["a","b"]}`)
	if _, _, err := load(noaddrs, merge("is_tor_exit_node")); err == nil {
		t.Error("expected an error for JSON with no addresses")
	}

	// Reserved networks stop the build unless -skip-reserved, which drops
	// them and counts them.
	reserved := write("reserved.txt", "1.2.3.4\n10.0.0.0/8\nfd00::/8\n")
	if _, _, err := load(reserved, merge("is_anonymous_vpn")); err == nil {
		t.Error("expected an error for a reserved network")
	}
	*skipReserved = true
	defer func() { *skipReserved = false }()
	n, skipped, err := load(reserved, merge("is_anonymous_vpn"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || skipped != 2 {
		t.Errorf("skip-reserved: loaded %d, skipped %d, want 1 and 2", n, skipped)
	}
}

// TestFormats checks the input formats against what their standards
// require: CIDR notation (RFC 4632), IPv6 text forms and IPv4-mapped
// addresses (RFC 4291, RFC 5952), geofeeds with an RFC 9632 signature
// trailer (RFC 8805), JSON (RFC 8259), and UTF-8 with or without a byte
// order mark (RFC 3629). No tree is involved, each case collects what
// load would insert.
func TestFormats(t *testing.T) {
	dir := t.TempDir()
	geofeed := "# geofeed per RFC 8805\r\n\r\n5.6.7.0/24,US,US-NJ,Newark,\r\n5.6.8.1,us,,,\r\n" +
		"5.6.9.0/24,DE,DE-BE,Berlin # inline\r\n# RPKI Signature: 5.6.0.0/16\r\n" +
		"# MIIGlwYJKoZIhvcNAQcCoIIGiDCCBoQCAQExDTALBglghkgBZQMEAgEwDQYLKoZI\r\n# End Signature: 5.6.0.0/16\r\n"
	for _, tc := range []struct {
		name, body string
		want       []string // prefixes in order, nil means an error
	}{
		{"cidr", "1.2.3.0/24\n", []string{"1.2.3.0/24"}},
		{"host bits masked", "1.2.3.4/24\n", []string{"1.2.3.0/24"}},
		{"bare address", "1.2.3.4\n", []string{"1.2.3.4/32"}},
		{"prefix too long", "1.2.3.0/33\n", nil},
		{"ipv6 expanded uppercase", "2A00:1450:0000:0000:0000:0000:0000:0001\n", []string{"2a00:1450::1/128"}},
		{"ipv6 network", "2a00:1450::/32\n", []string{"2a00:1450::/32"}},
		{"ipv4 mapped address", "::ffff:1.2.3.4\n", []string{"1.2.3.4/32"}},
		{"ipv4 mapped network", "::ffff:1.2.3.0/120\n", []string{"1.2.3.0/24"}},
		{"zone refused", "2a00:1450::1%eth0\n", nil},
		{"brackets refused", "[2a00:1450::1]\n", nil},
		{"geofeed", geofeed, []string{"5.6.7.0/24", "5.6.8.1/32", "5.6.9.0/24"}},
		{"bom text", "\ufeff1.2.3.4\n", []string{"1.2.3.4/32"}},
		{"json nested escaped duplicate", `{"a":{"b":["1.2.3.0\/24"]},"n":12345,"s":"see 5.6.7.8 inside","k":"9.9.9.9","k":"::ffff:9.9.9.10"}`,
			[]string{"1.2.3.0/24", "9.9.9.9/32", "9.9.9.10/32"}},
		{"json array", `["1.1.1.1", 7, null]`, []string{"1.1.1.1/32"}},
		{"json broken", `{"a":`, nil},
		{"json truncated", `{"a":"1.1.1.1"`, nil},
		{"invalid utf-8", "1.2.3.4\n\xff\xfe5.6.7.8\n", nil},
		{"utf-16", "\xff\xfe1\x00.\x002\x00.\x003\x00.\x004\x00\n\x00", nil},
		{"comments only", "# nothing\n\n", nil},
	} {
		path := filepath.Join(dir, strings.ReplaceAll(tc.name, " ", "_"))
		if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
			t.Fatal(err)
		}
		var got []string
		_, _, err := load(path, func(p netip.Prefix) error { got = append(got, p.Masked().String()); return nil })
		switch {
		case tc.want == nil && err == nil:
			t.Errorf("%s: expected an error, got %v", tc.name, got)
		case tc.want != nil && err != nil:
			t.Errorf("%s: %v", tc.name, err)
		case tc.want != nil && !slices.Equal(got, tc.want):
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestRun builds a database through run and checks what the MaxMind DB
// specification asks of the metadata, that nothing is left beside the
// output, and that an exclude which removes a list's first entry does not
// fail the verification.
func TestRun(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// The exclude takes the Tor list's first entry, so verification has
	// to fall through to its second.
	steps := []step{
		{"tor", "is_tor_exit_node", write("tor.txt", "185.220.101.1\n185.220.101.3\n"), extras{}},
		{"hosting", "is_hosting_provider", write("dc.txt", "185.220.101.0/24\n"), extras{}},
		{"exclude", "", write("ex.txt", "185.220.101.1\n"), extras{}},
	}
	out := filepath.Join(dir, "out.mmdb")
	o := options{steps, out, modeBase.dbType(), describe(steps, modeBase), 28, modeBase}
	if err := run(o); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(out + ".tmp"); err == nil {
		t.Error("temporary file left beside the output")
	}

	db, err := maxminddb.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	m := db.Metadata
	if m.DatabaseType != o.dbType {
		t.Errorf("database_type %q", m.DatabaseType)
	}
	if m.Description["en"] != "open-anonymous-ip dev - tor, hosting, exclude" {
		t.Errorf("description %v", m.Description)
	}
	if !slices.Equal(m.Languages, []string{"en"}) {
		t.Errorf("languages %v", m.Languages)
	}
	if m.IPVersion != 6 || m.RecordSize != 28 {
		t.Errorf("ip_version %d, record_size %d", m.IPVersion, m.RecordSize)
	}
	if age := time.Since(time.Unix(int64(m.BuildEpoch), 0)); age < 0 || age > 2*time.Minute {
		t.Errorf("build_epoch %d is not now", m.BuildEpoch)
	}
	// Every address not in any list, and every excluded address, has an
	// empty record, as in MaxMind's files, so readers that treat a miss as
	// an error never hit one.
	for _, addr := range []string{"185.220.101.1", "8.8.8.8", "2001:4860:4860::8888"} {
		var rec map[string]bool
		res := db.Lookup(netip.MustParseAddr(addr))
		if err := res.Decode(&rec); err != nil || !res.Found() || len(rec) != 0 {
			t.Errorf("%s: want an empty record, got %v, found %v, %v", addr, rec, res.Found(), err)
		}
	}
	var rec map[string]bool
	if err := db.Lookup(netip.MustParseAddr("185.220.101.2")).Decode(&rec); err != nil || !rec["is_hosting_provider"] || rec["is_tor_exit_node"] {
		t.Errorf("185.220.101.2: got %v, %v", rec, err)
	}

	// The specification allows 24, 28, or 32 bit records and nothing else.
	o.recordSize = 20
	if err := run(o); err == nil {
		t.Error("expected an error for record size 20")
	}
}

// plusRec is a record in the Anonymous Plus or IP Risk layout.
type plusRec struct {
	Tor        bool    `maxminddb:"is_tor_exit_node"`
	VPN        bool    `maxminddb:"is_anonymous_vpn"`
	Hosting    bool    `maxminddb:"is_hosting_provider"`
	Confidence uint16  `maxminddb:"anonymizer_confidence"`
	Seen       string  `maxminddb:"network_last_seen"`
	Provider   string  `maxminddb:"provider_name"`
	Risk       float64 `maxminddb:"ip_risk"`
}

// TestModes covers the Anonymous Plus and IP Risk layouts: the extras a
// list is given land in its records with MaxMind's types, a later list
// overrides a shared network's extras, and the extras are refused in a
// mode that does not carry them.
func TestModes(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	tor := write("tor.txt", "185.220.101.1\n")
	vpn := write("vpn.txt", "1.12.0.0/14\n")
	hosting := write("dc.txt", "1.12.0.0/14\n")
	out := filepath.Join(dir, "out.mmdb")
	lookup := func(db *maxminddb.Reader, addr string) plusRec {
		var rec plusRec
		if err := db.Lookup(netip.MustParseAddr(addr)).Decode(&rec); err != nil {
			t.Fatalf("%s: %v", addr, err)
		}
		return rec
	}

	// Plus: confidence and last seen on Tor, a provider on the VPN list,
	// which also overrides the hosting list's record on the shared network.
	steps := []step{
		{"tor", "is_tor_exit_node", tor, extras{confidence: 99, seen: "2026-10-29"}},
		{"hosting", "is_hosting_provider", hosting, extras{}},
		{"vpn", "is_anonymous_vpn", vpn, extras{confidence: 30, provider: "mullvad"}},
	}
	if err := run(options{steps, out, modePlus.dbType(), describe(steps, modePlus), 24, modePlus}); err != nil {
		t.Fatal(err)
	}
	db, err := maxminddb.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	if db.Metadata.DatabaseType != "Open-Anonymous-IP-Plus (compat=GeoIP-Anonymous-Plus)" {
		t.Errorf("database_type %q", db.Metadata.DatabaseType)
	}
	if rec := lookup(db, "185.220.101.1"); !rec.Tor || rec.Confidence != 99 || rec.Seen != "2026-10-29" || rec.Provider != "" || rec.Risk != 0 {
		t.Errorf("tor: %+v", rec)
	}
	if rec := lookup(db, "1.13.0.1"); !rec.VPN || !rec.Hosting || rec.Confidence != 30 || rec.Provider != "mullvad" || rec.Seen != "" {
		t.Errorf("vpn over hosting: %+v", rec)
	}
	if db.Metadata.Description["en"] != "open-anonymous-ip dev plus - vpn, tor, hosting" {
		t.Errorf("description %v", db.Metadata.Description)
	}
	// Plus carries the empty default record, as MaxMind's file does.
	if res := db.Lookup(netip.MustParseAddr("8.8.8.8")); !res.Found() {
		t.Error("8.8.8.8: want an empty record in plus")
	}
	// The raw record must carry MaxMind's types, not strings that look like them.
	var raw map[string]any
	if err := db.Lookup(netip.MustParseAddr("185.220.101.1")).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	switch raw["anonymizer_confidence"].(type) {
	case uint16, uint32, uint64:
	default:
		t.Errorf("anonymizer_confidence decoded as %T", raw["anonymizer_confidence"])
	}
	if _, ok := raw["network_last_seen"].(string); !ok {
		t.Errorf("network_last_seen decoded as %T", raw["network_last_seen"])
	}
	_ = db.Close()

	// Risk: ip_risk is a double, and the plus fields stay.
	steps = []step{{"tor", "is_tor_exit_node", tor, extras{confidence: 99, score: 87.5}}}
	if err := run(options{steps, out, modeRisk.dbType(), describe(steps, modeRisk), 24, modeRisk}); err != nil {
		t.Fatal(err)
	}
	db, err = maxminddb.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	if db.Metadata.DatabaseType != "Open-IP-Risk (compat=GeoIP2-IP-Risk)" {
		t.Errorf("database_type %q", db.Metadata.DatabaseType)
	}
	if rec := lookup(db, "185.220.101.1"); !rec.Tor || rec.Confidence != 99 || rec.Risk != 87.5 {
		t.Errorf("risk: %+v", rec)
	}
	if err := db.Lookup(netip.MustParseAddr("185.220.101.1")).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["ip_risk"].(float64); !ok {
		t.Errorf("ip_risk decoded as %T", raw["ip_risk"])
	}
	// IP Risk carries no empty default record, as MaxMind's file does not.
	if db.Lookup(netip.MustParseAddr("8.8.8.8")).Found() {
		t.Error("8.8.8.8: expected no record in risk")
	}
	_ = db.Close()

	// Extras are refused where the layout has no field for them.
	if err := run(options{steps, out, modePlus.dbType(), describe(steps, modePlus), 24, modePlus}); err == nil {
		t.Error("expected an error for -score without -risk")
	}
	steps = []step{{"tor", "is_tor_exit_node", tor, extras{seen: "2026-10-29"}}}
	if err := run(options{steps, out, modeBase.dbType(), describe(steps, modeBase), 24, modeBase}); err == nil {
		t.Error("expected an error for -seen without -plus")
	}
}

// FuzzLoad feeds load arbitrary bytes. It must return an error or valid,
// masked, zone-free prefixes in the family readers expect, and never panic.
func FuzzLoad(f *testing.F) {
	for _, seed := range []string{
		"1.2.3.0/24\n::ffff:1.2.3.4\n2a00:1450::/32\n",
		"# geofeed\r\n5.6.7.0/24,US,US-NJ,Newark,\r\n",
		"\ufeff[\"9.9.9.9\"]",
		`{"a":{"b":["1.2.3.0\/24"]},"n":1}`,
		"\xff\xfe1\x00",
		"1.2.3.0/33\n",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		path := filepath.Join(t.TempDir(), "in")
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, _ = load(path, func(p netip.Prefix) error {
			if !p.IsValid() || p != p.Masked() || p.Addr().Is4In6() || p.Addr().Zone() != "" {
				t.Fatalf("bad prefix %v", p)
			}
			return nil
		})
	})
}
