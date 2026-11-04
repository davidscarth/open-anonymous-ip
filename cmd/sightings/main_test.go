// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseProxyLine(t *testing.T) {
	for _, tc := range []struct {
		line, want string
	}{
		{"socks5://1.2.3.4:1080", "1.2.3.4"},
		{"http://[2001:db8::1]:8080", "2001:db8::1"},
		{"1.2.3.4:3128", "1.2.3.4"},
		{"[2001:db8::2]:3128", "2001:db8::2"},
		{"2001:db8::3", "2001:db8::3"},
		{"  1.2.3.5  # comment", "1.2.3.5"},
		{"::ffff:1.2.3.6", "1.2.3.6"},
		{"", ""},
		{"# comment", ""},
		{"not an address", ""},
		{"fe80::1%eth0", ""},
	} {
		got, ok := parseProxyLine(tc.line)
		if tc.want == "" {
			if ok {
				t.Errorf("%q: parsed as %s, want refusal", tc.line, got)
			}
			continue
		}
		if !ok || got.String() != tc.want {
			t.Errorf("%q: got %s %v, want %s", tc.line, got, ok, tc.want)
		}
	}
}

func TestParseStormsia(t *testing.T) {
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	body := `[{"protocol":"http","host":"10.0.0.1","port":80,"exit_ip":"203.0.113.9","asn":{"autonomous_system_number":1}},
	{"protocol":"socks5","host":"10.0.0.2","port":1080,"exit_ip":"10.0.0.2"},
	{"protocol":"socks4","host":"10.0.0.3","port":1080,"exit_ip":null},
	{"protocol":"socks4","host":"bad","port":1080}]`
	got := parseStormsia([]byte(body), at)
	var want []sighting
	for _, s := range []struct {
		a    string
		exit bool
	}{{"10.0.0.1", false}, {"203.0.113.9", true}, {"10.0.0.2", false}, {"10.0.0.2", true}, {"10.0.0.3", false}} {
		want = append(want, sighting{netip.MustParseAddr(s.a), at, s.exit})
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if parseStormsia([]byte("not json"), at) != nil {
		t.Error("bad JSON should give nothing")
	}
}

// TestConfidence checks every cell of the table against the methodology,
// and the edges between rows and columns.
func TestConfidence(t *testing.T) {
	for _, tc := range []struct {
		hours float64
		days  int
		want  int
	}{
		{0, 1, 70}, {0, 2, 70}, {0, 3, 70}, {0, 4, 99}, {23.9, 9, 99},
		{24, 1, 30}, {24, 2, 50}, {47, 4, 70},
		{48, 1, 30}, {48, 3, 30}, {119, 4, 70},
		{120, 1, 10}, {120, 2, 30}, {191, 4, 50},
		{192, 1, 10}, {192, 3, 30}, {335, 4, 30},
		{336, 1, 10}, {336, 2, 10}, {719, 4, 10},
		{720, 4, 0}, {1000, 1, 0},
	} {
		if got := confidence(tc.hours, tc.days); got != tc.want {
			t.Errorf("confidence(%v, %d) = %d, want %d", tc.hours, tc.days, got, tc.want)
		}
	}
}

func TestGoogleOwn(t *testing.T) {
	p := func(s string) netip.Prefix { return netip.MustParsePrefix(s) }
	got := googleOwn(
		[]netip.Prefix{p("8.8.8.0/24"), p("34.0.0.0/15"), p("2001:4860::/32")},
		[]netip.Prefix{p("34.1.0.0/16"), p("2001:4860:8000::/33")},
	)
	want := []netip.Prefix{p("8.8.8.0/24"), p("34.0.0.0/16"), p("2001:4860::/33")}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestOnionooIPv6(t *testing.T) {
	body := `{"relays":[{"or_addresses":["1.2.3.4:9001","[2a0b:f4c2::1]:9001"]},
	{"or_addresses":["[2a0b:f4c2::1]:443","[2a0b:f4c2::2]:9001"]},{"or_addresses":["5.6.7.8:9001"]}]}`
	got, err := onionooIPv6([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	want := []netip.Addr{netip.MustParseAddr("2a0b:f4c2::1"), netip.MustParseAddr("2a0b:f4c2::2")}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if _, err := onionooIPv6([]byte("[]")); err == nil {
		t.Error("an array is not a details document")
	}
}

func TestSelectASNs(t *testing.T) {
	body := `[{"asn":1,"metadata":{"category":"isp"}},{"asn":2,"metadata":{"category":"hosting"}},
	{"asn":3,"metadata":{"category":null}},{"asn":4,"metadata":{"category":"isp"}}]`
	asns, total, err := selectASNs(strings.NewReader(body), "isp")
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 || !slices.Equal(asns, []int{1, 4}) {
		t.Errorf("got %v of %d", asns, total)
	}
	if _, _, err := selectASNs(strings.NewReader(`{"asn":1}`), "isp"); err == nil {
		t.Error("an object is not as.json")
	}
}

func TestRollup(t *testing.T) {
	in := "is_hosting_provider    44669 entries  lists/x4bnet-datacenter-ipv4.txt\n" +
		"is_public_proxy          100 entries  plus/public-proxy-2026-10-05-c10.txt\n" +
		"is_public_proxy          200 entries  plus/public-proxy-2026-10-06-c10.txt  (1 reserved skipped)\n" +
		"is_public_proxy         3000 entries  plus/public-proxy-2026-11-03-c99.txt\n" +
		"is_residential_proxy      50 entries  plus/residential-2026-10-05-c10.txt\n" +
		"is_residential_proxy    1500 entries  plus/residential-2026-11-03-c99.txt\n" +
		"is_tor_exit_node        1200 entries  lists/torproject-exits.txt\n" +
		"out.mmdb  123 bytes  5 flagged networks  9 nodes  Open-Anonymous-IP-Plus (compat=GeoIP-Anonymous-Plus)\n"
	want := "is_hosting_provider    44669 entries  lists/x4bnet-datacenter-ipv4.txt\n" +
		"is_public_proxy         3000 entries  plus/public-proxy-*-c99.txt (1 files)\n" +
		"is_public_proxy          300 entries  plus/public-proxy-*-c10.txt (2 files)\n" +
		"is_residential_proxy    1500 entries  plus/residential-*-c99.txt (1 files)\n" +
		"is_residential_proxy      50 entries  plus/residential-*-c10.txt (1 files)\n" +
		"is_tor_exit_node        1200 entries  lists/torproject-exits.txt\n" +
		"out.mmdb  123 bytes  5 flagged networks  9 nodes  Open-Anonymous-IP-Plus (compat=GeoIP-Anonymous-Plus)\n"
	if got := rollup(in); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// git makes a repository in a temp dir and returns a function that
// commits files at a given time.
func gitRepo(t *testing.T) (string, func(at time.Time, files map[string]string)) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	run := func(at time.Time, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
			"GIT_AUTHOR_DATE="+strconv.FormatInt(at.Unix(), 10)+" +0000",
			"GIT_COMMITTER_DATE="+strconv.FormatInt(at.Unix(), 10)+" +0000",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(time.Unix(0, 0), "init", "-q")
	return dir, func(at time.Time, files map[string]string) {
		t.Helper()
		for name, content := range files {
			p := filepath.Join(dir, name)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		run(at, "add", "-A")
		run(at, "commit", "-q", "--allow-empty", "-m", at.Format(time.RFC3339))
	}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(body))
}

// TestHistory builds two small proxy-list repositories with commits at
// known times and checks the lists history writes from them: the merged
// windows, the per-day confidence files, and which addresses count as
// residential.
func TestHistory(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	iplocate, commitIPL := gitRepo(t)
	stormsia, commitSt := gitRepo(t)

	// 198.51.100.1: listed by both on ten days, last seen 2 hours ago.
	// 198.51.100.2: IPLocate only, two days, last 3 days ago, not an ISP.
	// 203.0.113.5: stormsia exit on an ISP network, four days in the
	// window, last seen 20 days ago, so in Plus only.
	// 203.0.113.6: exit on an ISP network that is also a hosting range.
	// 192.0.2.1: host in a hosting range.
	// 192.0.2.9: host 40 days ago, outside every window.
	// 198.51.100.3: host on an ISP network, seen once, 20 hours ago.
	// Every snapshot differs, as real ones do: git log only lists the
	// commits that changed the file.
	for _, d := range []int{25, 10} {
		commitIPL(now.Add(-time.Duration(d)*day), map[string]string{
			"all-proxies.txt": "socks5://198.51.100.1:" + strconv.Itoa(1000+d) + "\n",
		})
	}
	for _, d := range []int{5, 3} {
		commitIPL(now.Add(-time.Duration(d)*day), map[string]string{
			"all-proxies.txt": "socks5://198.51.100.1:" + strconv.Itoa(1000+d) + "\nhttp://198.51.100.2:8080\n",
		})
	}
	commitIPL(now.Add(-2*time.Hour), map[string]string{"all-proxies.txt": "socks5://198.51.100.1:1080\n"})
	commitSt(now.Add(-40*day), map[string]string{
		"web/public/proxies.json": `[{"host":"192.0.2.9","exit_ip":"192.0.2.9"}]`,
	})
	for _, d := range []int{26, 24, 22, 20} {
		commitSt(now.Add(-time.Duration(d)*day), map[string]string{
			"web/public/proxies.json": `[{"host":"198.51.100.1","port":` + strconv.Itoa(d) + `,"exit_ip":"203.0.113.5"},{"host":"192.0.2.1","exit_ip":"203.0.113.6"}]`,
		})
	}
	commitSt(now.Add(-20*time.Hour), map[string]string{
		"web/public/proxies.json": `[{"host":"198.51.100.3","exit_ip":"198.51.100.1"}]`,
	})

	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	isp := write("isp.txt", "# isp\n203.0.113.0/24\n198.51.100.1\n198.51.100.3\n")
	hosting := write("dc.txt", "203.0.113.6/32\n192.0.2.0/24\n")
	out, plus := filepath.Join(dir, "lists"), filepath.Join(dir, "plus")
	if err := historyCmd([]string{"-iplocate", iplocate, "-stormsia", stormsia, "-isp", isp, "-hosting", hosting,
		"-out", out, "-plus", plus, "-now", now.Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}

	// The base window is 14 days, so the addresses last seen 20 days ago
	// are out.
	if got := readLines(t, filepath.Join(out, "public-proxy.txt")); !slices.Equal(got, []string{"198.51.100.1", "198.51.100.2", "198.51.100.3"}) {
		t.Errorf("public-proxy.txt: %v", got)
	}
	// 198.51.100.1 and .3 are on the ISP list, .2 is not, 203.0.113.6 is
	// hosting, and 203.0.113.5 is too old for the base window.
	if got := readLines(t, filepath.Join(out, "residential.txt")); !slices.Equal(got, []string{"198.51.100.1", "198.51.100.3"}) {
		t.Errorf("residential.txt: %v", got)
	}
	want := map[string][]string{
		"public-proxy-2026-11-03-c99.txt": {"198.51.100.1"}, // 2h ago, ten days across both lists
		"public-proxy-2026-10-31-c30.txt": {"198.51.100.2"}, // 72h ago, two days listed
		"public-proxy-2026-11-02-c70.txt": {"198.51.100.3"}, // 20h ago, one day
		"public-proxy-2026-10-14-c10.txt": {"192.0.2.1", "203.0.113.5", "203.0.113.6"},
		"residential-2026-11-03-c99.txt":  {"198.51.100.1"},
		"residential-2026-11-02-c70.txt":  {"198.51.100.3"},
		"residential-2026-10-14-c10.txt":  {"203.0.113.5"},
	}
	entries, err := os.ReadDir(plus)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		got := readLines(t, filepath.Join(plus, e.Name()))
		w, ok := want[e.Name()]
		if !ok {
			t.Errorf("unexpected %s: %v", e.Name(), got)
			continue
		}
		if !slices.Equal(got, w) {
			t.Errorf("%s: got %v, want %v", e.Name(), got, w)
		}
		delete(want, e.Name())
	}
	for name := range want {
		t.Errorf("missing %s", name)
	}
}

// TestISP builds an as-metadata and an as-ip-blocks repository and checks
// that only the isp ASNs' prefixes come out, in both families.
func TestISP(t *testing.T) {
	meta, commitMeta := gitRepo(t)
	blocks, commitBlocks := gitRepo(t)
	at := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	commitMeta(at, map[string]string{"as.json": `[
	{"asn":7922,"metadata":{"category":"isp"}},
	{"asn":14061,"metadata":{"category":"hosting"}},
	{"asn":64496,"metadata":{"category":"isp"}},
	{"asn":64497,"metadata":{"category":"isp"}}]`})
	commitBlocks(at, map[string]string{
		"as/7922/ipv4-aggregated.txt":  "# AS7922\n23.24.0.0/15\n23.30.0.0/15\n",
		"as/7922/ipv6-aggregated.txt":  "2001:558::/29\n",
		"as/14061/ipv4-aggregated.txt": "64.225.0.0/17\n",
		"as/64496/ipv4-aggregated.txt": "198.51.100.0/24\n",
		// 64497 announces nothing and has no directory.
	})
	out := filepath.Join(t.TempDir(), "isp.txt")
	if err := ispCmd([]string{"-metadata", meta, "-blocks", blocks, "-out", out}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if !strings.HasPrefix(lines[0], "# prefixes announced by the 3 ASNs") {
		t.Errorf("header %q", lines[0])
	}
	if got := lines[1:]; !slices.Equal(got, []string{"23.24.0.0/15", "23.30.0.0/15", "2001:558::/29", "198.51.100.0/24"}) {
		t.Errorf("prefixes %v", got)
	}
	set, err := readSet([]string{out})
	if err != nil {
		t.Fatal(err)
	}
	if !set.Contains(netip.MustParseAddr("23.31.1.1")) || set.Contains(netip.MustParseAddr("64.225.1.1")) {
		t.Error("set does not match the isp prefixes")
	}
}
