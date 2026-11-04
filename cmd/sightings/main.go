// SPDX-License-Identifier: Apache-2.0

// sightings prepares the lists the daily build feeds to open-anonymous-ip.
// It is the data handling the workflow used to do inline, in one place
// with tests.
//
//	sightings history -iplocate REPO -stormsia REPO -hosting dc4.txt -hosting dc6.txt -isp isp.txt -out lists -plus plus
//	sightings isp -metadata REPO -blocks REPO -out lists/ipverse-isp.txt
//	sightings google -goog goog.json -cloud cloud.json -out lists/google-own.txt
//	sightings onionoo -in details.json -out lists/onionoo-exits-ipv6.txt
//	sightings notes -in build-plus.txt
//
// history walks the git history of the proxy lists, IPLocate's
// all-proxies.txt and stormsia's proxies.json, and folds every sighting
// of an address, as a proxy host or as the exit a proxy was seen leaving
// from, into one record per address: when it was last listed and on how
// many distinct days in the window. From that it writes the public proxy
// list for the base file, every address listed in the last 14 days, and
// for the Plus file one list per last-listed day and confidence, with the
// confidence from the table in docs/methodology.md. Addresses on an ISP
// network, and not in a hosting range, go to the residential lists as
// well, with the same dates and confidences.
//
// isp writes the announced prefixes of every ASN that ipverse's
// as-metadata puts in the isp category, read from ipverse's as-ip-blocks.
// Both repositories are read through git, so a clone without a checkout
// is enough. google writes Google's own ranges, everything in goog.json
// that is not in cloud.json. onionoo takes the IPv6 addresses of the
// relays in an Onionoo details document. notes rolls the per-day lines of
// the Plus build output up to one line per list and confidence, for the
// release notes.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"go4.org/netipx"
)

const day = 24 * time.Hour

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "history":
		err = historyCmd(os.Args[2:])
	case "isp":
		err = ispCmd(os.Args[2:])
	case "google":
		err = googleCmd(os.Args[2:])
	case "onionoo":
		err = onionooCmd(os.Args[2:])
	case "notes":
		err = notesCmd(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "sightings:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, "usage: sightings history|isp|google|onionoo|notes [flags]\n\n"+
		"  history  fold the proxy lists' git histories into the public proxy and residential lists\n"+
		"  isp      write the prefixes of ipverse's isp ASNs\n"+
		"  google   write Google's own ranges, goog.json minus cloud.json\n"+
		"  onionoo  write the IPv6 addresses of an Onionoo details document\n"+
		"  notes    roll the Plus build output up for the release notes\n")
	os.Exit(2)
}

// stringList is a repeatable string flag.
type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// history

// confidence is the table under anonymizer_confidence in
// docs/methodology.md: rows are hours since last listed, columns distinct
// days listed in the 30-day window (1, 2-3, 4+), the value the share of
// such addresses listed again within 14 days, to the nearest of 99, 70,
// 50, 30, and 10, with 99 kept for addresses checked within the day.
var confidenceEdges = []float64{24, 48, 120, 192, 336, 720}

var confidenceTable = [6][3]int{
	{70, 70, 99}, // under 24h
	{30, 50, 70}, // 24-48h
	{30, 30, 70}, // 48-120h
	{10, 30, 50}, // 120h-8 days
	{10, 30, 30}, // 8-14 days
	{10, 10, 10}, // 15-30 days
}

// confidence returns the value for an address last listed hours ago and
// listed on days distinct days, or 0 past the end of the table.
func confidence(hours float64, days int) int {
	row := 0
	for row < len(confidenceEdges) && hours >= confidenceEdges[row] {
		row++
	}
	if row >= len(confidenceEdges) {
		return 0
	}
	col := 0
	switch {
	case days >= 4:
		col = 2
	case days >= 2:
		col = 1
	}
	return confidenceTable[row][col]
}

// A record is everything history keeps per address.
type record struct {
	last time.Time
	days map[int64]struct{} // UTC day numbers listed
	exit bool               // seen as an exit, not only as a proxy host
}

// sighting is one appearance of an address in one snapshot.
type sighting struct {
	addr netip.Addr
	at   time.Time
	exit bool
}

type histories map[netip.Addr]*record

func (h histories) add(s sighting) {
	r := h[s.addr]
	if r == nil {
		r = &record{days: map[int64]struct{}{}}
		h[s.addr] = r
	}
	if s.at.After(r.last) {
		r.last = s.at
	}
	r.days[s.at.Unix()/86400] = struct{}{}
	r.exit = r.exit || s.exit
}

func historyCmd(args []string) error {
	fs := flag.NewFlagSet("history", flag.ExitOnError)
	iplocate := fs.String("iplocate", "", "clone of iplocate/free-proxy-list")
	stormsia := fs.String("stormsia", "", "clone of stormsia/proxy-list")
	var hosting stringList
	fs.Var(&hosting, "hosting", "hosting ranges, addresses in them are never residential (repeatable)")
	isp := fs.String("isp", "", "ISP prefixes, from sightings isp")
	out := fs.String("out", "", "directory for public-proxy.txt and residential.txt")
	plus := fs.String("plus", "", "directory for the per-day Plus lists")
	nowFlag := fs.String("now", "", "build time, RFC 3339, default now")
	base := fs.Int("base", 14, "days in the base window")
	window := fs.Int("window", 30, "days in the Plus window")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if (*iplocate == "" && *stormsia == "") || *out == "" || *plus == "" {
		return errors.New("history needs -iplocate or -stormsia, -out, and -plus")
	}
	now := time.Now()
	if *nowFlag != "" {
		var err error
		if now, err = time.Parse(time.RFC3339, *nowFlag); err != nil {
			return err
		}
	}
	now = now.UTC()
	since := now.Add(-time.Duration(*window) * day)

	hostingSet, err := readSet(hosting)
	if err != nil {
		return err
	}
	var ispSet *netipx.IPSet
	if *isp != "" {
		if ispSet, err = readSet([]string{*isp}); err != nil {
			return err
		}
	}

	h := histories{}
	var snaps, rows, bad int
	if *iplocate != "" {
		n, err := walk(*iplocate, "all-proxies.txt", since, func(at time.Time, body []byte) {
			for _, line := range strings.Split(string(body), "\n") {
				addr, ok := parseProxyLine(line)
				if strings.TrimSpace(line) != "" && !ok {
					bad++
					continue
				}
				if ok {
					h.add(sighting{addr, at, false})
					rows++
				}
			}
		})
		if err != nil {
			return fmt.Errorf("iplocate: %w", err)
		}
		fmt.Printf("iplocate  %5d snapshots since %s\n", n, since.Format(time.RFC3339))
		snaps += n
	}
	if *stormsia != "" {
		n, err := walk(*stormsia, "web/public/proxies.json", since, func(at time.Time, body []byte) {
			for _, s := range parseStormsia(body, at) {
				h.add(s)
				rows++
			}
		})
		if err != nil {
			return fmt.Errorf("stormsia: %w", err)
		}
		fmt.Printf("stormsia  %5d snapshots since %s\n", n, since.Format(time.RFC3339))
		snaps += n
	}
	if snaps == 0 {
		return errors.New("no snapshots in the window")
	}

	// Sort for stable output, then write the base lists and the per-day
	// Plus lists. An address is residential when it is on an ISP network
	// and not in a hosting range, whether it was listed as a proxy or seen
	// as one's exit.
	addrs := make([]netip.Addr, 0, len(h))
	for a := range h {
		addrs = append(addrs, a)
	}
	slices.SortFunc(addrs, netip.Addr.Compare)
	type key struct {
		name, date string
		conf       int
	}
	plusLists := map[key][]netip.Addr{}
	var baseProxy, baseRes []netip.Addr
	var exits, residential int
	for _, a := range addrs {
		r := h[a]
		if r.exit {
			exits++
		}
		res := ispSet != nil && ispSet.Contains(a) && !hostingSet.Contains(a)
		if res {
			residential++
		}
		if !r.last.Before(now.Add(-time.Duration(*base) * day)) {
			baseProxy = append(baseProxy, a)
			if res {
				baseRes = append(baseRes, a)
			}
		}
		c := confidence(now.Sub(r.last).Hours(), len(r.days))
		if c == 0 {
			continue
		}
		k := key{"public-proxy", r.last.Format("2006-01-02"), c}
		plusLists[k] = append(plusLists[k], a)
		if res {
			k.name = "residential"
			plusLists[k] = append(plusLists[k], a)
		}
	}
	if err := writeList(filepath.Join(*out, "public-proxy.txt"), baseProxy); err != nil {
		return err
	}
	if err := writeList(filepath.Join(*out, "residential.txt"), baseRes); err != nil {
		return err
	}
	keys := make([]key, 0, len(plusLists))
	for k := range plusLists {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b key) int {
		return strings.Compare(a.name+a.date+strconv.Itoa(a.conf), b.name+b.date+strconv.Itoa(b.conf))
	})
	for _, k := range keys {
		name := fmt.Sprintf("%s-%s-c%d.txt", k.name, k.date, k.conf)
		if err := writeList(filepath.Join(*plus, name), plusLists[k]); err != nil {
			return err
		}
	}
	fmt.Printf("%d sightings of %d addresses, %d seen as exits, %d residential (%d unparseable lines)\n",
		rows, len(h), exits, residential, bad)
	fmt.Printf("base: %d public proxy, %d residential; plus: %d lists\n", len(baseProxy), len(baseRes), len(keys))
	return nil
}

// walk calls fn with the content of path at every commit since the given
// time, newest first, and returns how many commits it saw.
func walk(repo, path string, since time.Time, fn func(at time.Time, body []byte)) (int, error) {
	log, err := exec.Command("git", "-C", repo, "log", "--format=%H %ct",
		"--since="+strconv.FormatInt(since.Unix(), 10), "--", path).Output()
	if err != nil {
		return 0, gitErr(err)
	}
	b, err := openBatch(repo)
	if err != nil {
		return 0, err
	}
	defer b.close()
	n := 0
	for line := range strings.SplitSeq(strings.TrimSpace(string(log)), "\n") {
		hash, ts, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		unix, err := strconv.ParseInt(ts, 10, 64)
		if err != nil {
			return n, fmt.Errorf("git log: %q", line)
		}
		at := time.Unix(unix, 0).UTC()
		if at.Before(since) { // --since is by committer date, and so is %ct, but be exact
			continue
		}
		body, found, err := b.read(hash + ":" + path)
		if err != nil {
			return n, err
		}
		if !found {
			continue
		}
		fn(at, body)
		n++
	}
	return n, nil
}

// parseProxyLine reads one line of a proxy list, scheme://host:port,
// host:port, [v6]:port, or a bare address, and returns the host.
func parseProxyLine(line string) (netip.Addr, bool) {
	s := strings.TrimSpace(line)
	if s == "" || s[0] == '#' {
		return netip.Addr{}, false
	}
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, " \t,#"); i >= 0 {
		s = s[:i]
	}
	switch {
	case strings.HasPrefix(s, "["):
		if i := strings.Index(s, "]"); i > 0 {
			s = s[1:i]
		}
	case strings.Count(s, ":") == 1:
		s = s[:strings.Index(s, ":")]
	}
	return parseAddr(s)
}

// parseAddr reads an address, unmapping IPv4 written in IPv6 form and
// refusing zones, as the tool does.
func parseAddr(s string) (netip.Addr, bool) {
	a, err := netip.ParseAddr(s)
	if err != nil || a.Zone() != "" {
		return netip.Addr{}, false
	}
	return a.Unmap(), true
}

// parseStormsia reads a proxies.json snapshot: each entry's host is a
// proxy, and its exit_ip, when present, is where that proxy's traffic was
// seen leaving from.
func parseStormsia(body []byte, at time.Time) []sighting {
	var entries []struct {
		Host string `json:"host"`
		Exit string `json:"exit_ip"`
	}
	if json.Unmarshal(body, &entries) != nil {
		return nil
	}
	var out []sighting
	for _, e := range entries {
		if a, ok := parseAddr(e.Host); ok {
			out = append(out, sighting{a, at, false})
		}
		if a, ok := parseAddr(e.Exit); ok {
			out = append(out, sighting{a, at, true})
		}
	}
	return out
}

// readSet builds a set from list files: one address or network per line,
// anything after a space, tab, comma, or # ignored, as the tool reads
// them. No files gives an empty set.
func readSet(paths []string) (*netipx.IPSet, error) {
	var b netipx.IPSetBuilder
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(f)
		for line := 1; sc.Scan(); line++ {
			s := strings.TrimSpace(sc.Text())
			if s == "" || s[0] == '#' {
				continue
			}
			if i := strings.IndexAny(s, " \t,#"); i >= 0 {
				s = s[:i]
			}
			p, err := netip.ParsePrefix(s)
			if err != nil {
				a, ok := parseAddr(s)
				if !ok {
					_ = f.Close()
					return nil, fmt.Errorf("%s: line %d: %q is not an address or network", path, line, s)
				}
				p = netip.PrefixFrom(a, a.BitLen())
			}
			if a := p.Addr(); a.Is4In6() && p.Bits() >= 96 {
				p = netip.PrefixFrom(a.Unmap(), p.Bits()-96)
			}
			b.AddPrefix(p.Masked())
		}
		if err := sc.Err(); err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		_ = f.Close()
	}
	return b.IPSet()
}

func writeList(path string, addrs []netip.Addr) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	for _, a := range addrs {
		buf.WriteString(a.String())
		buf.WriteByte('\n')
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// git plumbing

// batch is a git cat-file --batch process, one object per request.
type batch struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Reader
}

func openBatch(repo string) (*batch, error) {
	cmd := exec.Command("git", "-C", repo, "cat-file", "--batch")
	cmd.Stderr = os.Stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &batch{cmd, in, bufio.NewReaderSize(out, 1<<20)}, nil
}

// read returns the object at spec, such as HEAD:path or hash:path, and
// whether it exists.
func (b *batch) read(spec string) ([]byte, bool, error) {
	if _, err := io.WriteString(b.in, spec+"\n"); err != nil {
		return nil, false, err
	}
	head, err := b.out.ReadString('\n')
	if err != nil {
		return nil, false, fmt.Errorf("git cat-file: %w", err)
	}
	fields := strings.Fields(head)
	if len(fields) == 2 && fields[1] == "missing" {
		return nil, false, nil
	}
	if len(fields) != 3 {
		return nil, false, fmt.Errorf("git cat-file: %q", strings.TrimSpace(head))
	}
	size, err := strconv.Atoi(fields[2])
	if err != nil {
		return nil, false, fmt.Errorf("git cat-file: %q", strings.TrimSpace(head))
	}
	body := make([]byte, size+1) // content and the newline after it
	if _, err := io.ReadFull(b.out, body); err != nil {
		return nil, false, fmt.Errorf("git cat-file: %w", err)
	}
	return body[:size], true, nil
}

func (b *batch) close() {
	_ = b.in.Close()
	_ = b.cmd.Wait()
}

// blobHashes maps every file under dir at HEAD to its blob hash.
func blobHashes(repo, dir string) (map[string]string, error) {
	out, err := exec.Command("git", "-C", repo, "ls-tree", "-r", "-z", "HEAD", "--", dir).Output()
	if err != nil {
		return nil, gitErr(err)
	}
	blobs := map[string]string{}
	for entry := range strings.SplitSeq(strings.TrimSuffix(string(out), "\x00"), "\x00") {
		// mode type hash<TAB>path
		meta, path, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 || fields[1] != "blob" {
			continue
		}
		blobs[path] = fields[2]
	}
	return blobs, nil
}

func gitErr(err error) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		return fmt.Errorf("git: %s", strings.TrimSpace(string(ee.Stderr)))
	}
	return fmt.Errorf("git: %w", err)
}

// isp

func ispCmd(args []string) error {
	fs := flag.NewFlagSet("isp", flag.ExitOnError)
	metadata := fs.String("metadata", "", "clone of ipverse/as-metadata")
	blocks := fs.String("blocks", "", "clone of ipverse/as-ip-blocks")
	category := fs.String("category", "isp", "as-metadata category to select")
	out := fs.String("out", "", "output list")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *metadata == "" || *blocks == "" || *out == "" {
		return errors.New("isp needs -metadata, -blocks, and -out")
	}
	mb, err := openBatch(*metadata)
	if err != nil {
		return err
	}
	body, found, err := mb.read("HEAD:as.json")
	mb.close()
	if err != nil {
		return err
	}
	if !found {
		return errors.New("as.json not in as-metadata")
	}
	asns, total, err := selectASNs(bytes.NewReader(body), *category)
	if err != nil {
		return fmt.Errorf("as.json: %w", err)
	}
	if len(asns) == 0 {
		return fmt.Errorf("no ASNs in category %q", *category)
	}

	// The as directory holds a hundred thousand entries, and git parses
	// it again for every HEAD:as/... lookup, so list the tree once and
	// read the blobs by hash.
	blobs, err := blobHashes(*blocks, "as")
	if err != nil {
		return err
	}
	bb, err := openBatch(*blocks)
	if err != nil {
		return err
	}
	defer bb.close()
	var buf bytes.Buffer
	_, _ = fmt.Fprintf(&buf, "# prefixes announced by the %d ASNs ipverse/as-metadata lists as %s, from ipverse/as-ip-blocks (CC0)\n", len(asns), *category)
	var with, v4, v6 int
	for _, asn := range asns {
		seen := false
		for _, f := range []string{"ipv4-aggregated.txt", "ipv6-aggregated.txt"} {
			hash, ok := blobs[fmt.Sprintf("as/%d/%s", asn, f)]
			if !ok {
				continue
			}
			body, found, err := bb.read(hash)
			if err != nil {
				return err
			}
			if !found {
				continue
			}
			seen = true
			for line := range strings.SplitSeq(string(body), "\n") {
				line = strings.TrimSpace(line)
				if line == "" || line[0] == '#' {
					continue
				}
				p, err := netip.ParsePrefix(line)
				if err != nil {
					return fmt.Errorf("as/%d/%s: %q is not a network", asn, f, line)
				}
				if p.Addr().Is4() {
					v4++
				} else {
					v6++
				}
				buf.WriteString(p.Masked().String())
				buf.WriteByte('\n')
			}
		}
		if seen {
			with++
		}
	}
	if err := os.WriteFile(*out, buf.Bytes(), 0o644); err != nil {
		return err
	}
	fmt.Printf("%d ASNs, %d in category %s, %d with announced prefixes: %d IPv4 and %d IPv6 prefixes\n",
		total, len(asns), *category, with, v4, v6)
	return nil
}

// selectASNs streams as.json and returns the ASNs whose metadata.category
// matches, and how many ASNs the file holds.
func selectASNs(r io.Reader, category string) ([]int, int, error) {
	dec := json.NewDecoder(r)
	if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
		return nil, 0, errors.New("not a JSON array")
	}
	var asns []int
	total := 0
	for dec.More() {
		var rec struct {
			ASN      int `json:"asn"`
			Metadata struct {
				Category string `json:"category"`
			} `json:"metadata"`
		}
		if err := dec.Decode(&rec); err != nil {
			return nil, 0, err
		}
		total++
		if rec.Metadata.Category == category {
			asns = append(asns, rec.ASN)
		}
	}
	slices.Sort(asns)
	return asns, total, nil
}

// google

func googleCmd(args []string) error {
	fs := flag.NewFlagSet("google", flag.ExitOnError)
	goog := fs.String("goog", "", "goog.json, everything Google announces")
	cloud := fs.String("cloud", "", "cloud.json, Google Cloud customers")
	out := fs.String("out", "", "output list")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *goog == "" || *cloud == "" || *out == "" {
		return errors.New("google needs -goog, -cloud, and -out")
	}
	all, err := readGoogle(*goog)
	if err != nil {
		return err
	}
	customers, err := readGoogle(*cloud)
	if err != nil {
		return err
	}
	own := googleOwn(all, customers)
	var buf bytes.Buffer
	for _, p := range own {
		buf.WriteString(p.String())
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(*out, buf.Bytes(), 0o644); err != nil {
		return err
	}
	fmt.Printf("%d Google prefixes minus %d Google Cloud prefixes: %d prefixes of Google's own\n", len(all), len(customers), len(own))
	return nil
}

// readGoogle reads one of Google's range feeds, {"prefixes": [{"ipv4Prefix":
// ...} | {"ipv6Prefix": ...}]}.
func readGoogle(path string) ([]netip.Prefix, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Prefixes []struct {
			V4 string `json:"ipv4Prefix"`
			V6 string `json:"ipv6Prefix"`
		} `json:"prefixes"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var out []netip.Prefix
	for _, e := range doc.Prefixes {
		s := e.V4
		if s == "" {
			s = e.V6
		}
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return nil, fmt.Errorf("%s: %q is not a network", path, s)
		}
		out = append(out, p.Masked())
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: no prefixes", path)
	}
	return out, nil
}

// googleOwn is all minus customers, as the minimal list of prefixes.
func googleOwn(all, customers []netip.Prefix) []netip.Prefix {
	var b netipx.IPSetBuilder
	for _, p := range all {
		b.AddPrefix(p)
	}
	for _, p := range customers {
		b.RemovePrefix(p)
	}
	set, err := b.IPSet()
	if err != nil {
		return nil
	}
	return set.Prefixes()
}

// onionoo

func onionooCmd(args []string) error {
	fs := flag.NewFlagSet("onionoo", flag.ExitOnError)
	in := fs.String("in", "", "Onionoo details document")
	out := fs.String("out", "", "output list")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *in == "" || *out == "" {
		return errors.New("onionoo needs -in and -out")
	}
	body, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	addrs, err := onionooIPv6(body)
	if err != nil {
		return fmt.Errorf("%s: %w", *in, err)
	}
	if err := writeList(*out, addrs); err != nil {
		return err
	}
	fmt.Printf("%d IPv6 relay addresses\n", len(addrs))
	return nil
}

// onionooIPv6 returns the IPv6 addresses in the relays' or_addresses,
// [addr]:port entries, sorted and without duplicates. Nobody measures
// IPv6 exit traffic, so these stand in for an IPv6 exit list.
func onionooIPv6(body []byte) ([]netip.Addr, error) {
	var doc struct {
		Relays []struct {
			OrAddresses []string `json:"or_addresses"`
		} `json:"relays"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	set := map[netip.Addr]struct{}{}
	for _, r := range doc.Relays {
		for _, s := range r.OrAddresses {
			if !strings.HasPrefix(s, "[") {
				continue
			}
			if a, ok := parseProxyLine(s); ok && a.Is6() {
				set[a] = struct{}{}
			}
		}
	}
	addrs := make([]netip.Addr, 0, len(set))
	for a := range set {
		addrs = append(addrs, a)
	}
	slices.SortFunc(addrs, netip.Addr.Compare)
	return addrs, nil
}

// notes

// plusLine matches a build line for a per-day list, as the tool prints
// them: the field, the count, "entries", the path, and sometimes a
// reserved-skipped note.
var plusLine = regexp.MustCompile(`^(\S+)\s+(\d+) entries\s+(\S*/)?([a-z-]+)-\d{4}-\d{2}-\d{2}-c(\d+)\.txt(\s+\(\d+ reserved skipped\))?\s*$`)

func notesCmd(args []string) error {
	fs := flag.NewFlagSet("notes", flag.ExitOnError)
	in := fs.String("in", "", "Plus build output, default stdin")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var r io.Reader = os.Stdin
	if *in != "" {
		f, err := os.Open(*in)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		r = f
	}
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	fmt.Print(rollup(string(body)))
	return nil
}

// rollup replaces runs of per-day list lines with one line per field,
// list, and confidence, lists in the order they first appear and
// confidences 99 to 10, and leaves every other line as it is.
func rollup(body string) string {
	type key struct {
		field, dir, name string
		conf             int
	}
	var out strings.Builder
	var order []key
	sum := map[key]int{}
	files := map[key]int{}
	flush := func() {
		first := map[string]int{}
		for i, k := range order {
			if _, ok := first[k.name]; !ok {
				first[k.name] = i
			}
		}
		slices.SortStableFunc(order, func(a, b key) int {
			if d := first[a.name] - first[b.name]; d != 0 {
				return d
			}
			return b.conf - a.conf
		})
		for _, k := range order {
			_, _ = fmt.Fprintf(&out, "%-20s %7d entries  %s%s-*-c%d.txt (%d files)\n", k.field, sum[k], k.dir, k.name, k.conf, files[k])
		}
		order, sum, files = nil, map[key]int{}, map[key]int{}
	}
	for line := range strings.SplitSeq(strings.TrimSuffix(body, "\n"), "\n") {
		m := plusLine.FindStringSubmatch(line)
		if m == nil {
			flush()
			out.WriteString(line)
			out.WriteByte('\n')
			continue
		}
		n, _ := strconv.Atoi(m[2])
		conf, _ := strconv.Atoi(m[5])
		k := key{m[1], m[3], m[4], conf}
		if _, ok := sum[k]; !ok {
			order = append(order, k)
		}
		sum[k] += n
		files[k]++
	}
	flush()
	return out.String()
}
