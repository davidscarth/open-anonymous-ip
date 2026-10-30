// SPDX-License-Identifier: Apache-2.0

// open-anonymous-ip builds a MaxMind-compatible Anonymous IP database from plain
// lists of addresses and networks. Each list is given with the flag it
// should set, so one file can carry Tor exits, datacenter ranges, and
// VPN ranges at once. A list is one entry per line or any JSON document,
// so the cloud providers' published range feeds load as they are. An
// address in several lists gets every flag, and is_anonymous is set
// whenever any other flag is, as in MaxMind's file. Lists apply in
// command-line order, and -exclude removes networks from what came before.
//
//	open-anonymous-ip -tor tor.txt -hosting datacenter.txt -vpn vpn.txt -o anonymous.mmdb
//
// With -plus the records also carry MaxMind's Anonymous Plus fields.
// -confidence, -seen, and -provider set those for the lists that follow
// them. -risk, MaxMind's IP Risk layout, is in development and stops with
// an error.
//
// Any reader of GeoIP2 Anonymous IP databases reads the result, and
// caddy-geoip's geoip_anonymous matcher accepts it by type.
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
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/maxmind/mmdbwriter/v2"
	"github.com/maxmind/mmdbwriter/v2/inserter"
	"github.com/maxmind/mmdbwriter/v2/mmdbtype"
	"github.com/oschwald/maxminddb-golang/v2"
)

// kinds maps a flag name to the record field it sets, in MaxMind's order.
// -anonymous is for lists that say "anonymizer" without saying which kind.
type kind struct{ flag, field string }

var kinds = []kind{
	{"anonymous", "is_anonymous"},
	{"vpn", "is_anonymous_vpn"},
	{"tor", "is_tor_exit_node"},
	{"hosting", "is_hosting_provider"},
	{"public_proxy", "is_public_proxy"},
	{"residential_proxy", "is_residential_proxy"},
}

// extras are the Anonymous Plus and IP Risk fields a list's records carry,
// as set by -confidence, -seen, -provider, and -score before the list.
type extras struct {
	confidence     uint16
	seen, provider string
	score          float64
}

// A step is one list and what to do with it, in command-line order. An
// empty field means remove the list's networks from whatever came before.
type step struct {
	flag, field, path string
	extras            extras
}

// version is set at build time, go build -ldflags "-X main.version=abc1234".
var version = "dev"

// skipReserved drops private, loopback, documentation, and other reserved
// networks instead of stopping. Provider APIs sometimes list their tunnel
// addresses alongside their servers.
var skipReserved = flag.Bool("skip-reserved", false, "drop reserved networks with a count instead of stopping")

func main() {
	var steps []step
	var cur extras
	for _, k := range kinds {
		flag.Func(k.flag, "list of addresses or networks to flag as "+k.field+" (repeatable)", func(path string) error {
			steps = append(steps, step{k.flag, k.field, path, cur})
			return nil
		})
	}
	flag.Func("exclude", "list of networks to remove from the lists before it (repeatable)", func(path string) error {
		steps = append(steps, step{"exclude", "", path, extras{}})
		return nil
	})
	plus := flag.Bool("plus", false, "write Anonymous Plus records, with anonymizer_confidence, network_last_seen, and provider_name")
	risk := flag.Bool("risk", false, "write IP Risk records (in development)")
	flag.Func("confidence", "anonymizer_confidence, 1 to 99, for the lists after it, 0 to clear (needs -plus)", func(v string) error {
		n, err := strconv.ParseUint(v, 10, 16)
		if err != nil || n > 99 {
			return errors.New("want 0 to 99")
		}
		cur.confidence = uint16(n)
		return nil
	})
	flag.Func("seen", "network_last_seen, YYYY-MM-DD, for the lists after it, empty to clear (needs -plus)", func(v string) error {
		if v != "" {
			if _, err := time.Parse("2006-01-02", v); err != nil {
				return errors.New("want YYYY-MM-DD")
			}
		}
		cur.seen = v
		return nil
	})
	flag.Func("provider", "provider_name for the lists after it, empty to clear (needs -plus)", func(v string) error {
		cur.provider = v
		return nil
	})
	flag.Func("score", "ip_risk, 0.01 to 99, for the lists after it, 0 to clear (needs -risk)", func(v string) error {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f != 0 && (f < 0.01 || f > 99) {
			return errors.New("want 0.01 to 99, or 0")
		}
		cur.score = f
		return nil
	})
	out := flag.String("o", "", "output MMDB path (required)")
	dbType := flag.String("type", "", "database_type in the metadata, default Open-Anonymous-IP, -Plus, or Open-IP-Risk by mode, each with its compat suffix")
	desc := flag.String("description", "", "description in the metadata, default names the build and the flags given")
	recordSize := flag.Int("r", 24, "record size, 24, 28, or 32")
	flag.Usage = func() {
		_, _ = fmt.Fprintf(flag.CommandLine.Output(), "usage: open-anonymous-ip [flags] -o file.mmdb\n\n"+
			"Builds a MaxMind-compatible Anonymous IP database from lists of addresses\n"+
			"and networks. Lists apply in command-line order.\n\n"+
			"  open-anonymous-ip -tor tor.txt -hosting dc.txt -vpn vpn.txt -exclude relay.csv -o out.mmdb\n"+
			"  open-anonymous-ip -plus -provider mullvad -vpn mullvad.txt -provider '' -confidence 99 -seen 2026-10-29 -tor tor.txt -o plus.mmdb\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *out == "" || flag.NArg() > 0 {
		flag.Usage()
		os.Exit(2)
	}
	if *risk {
		fail(errors.New("-risk is in development, current tool supports base and plus only"))
	}
	m := modeBase
	if *plus {
		m = modePlus
	}
	if *dbType == "" {
		*dbType = m.dbType()
	}
	if *desc == "" {
		*desc = describe(steps, m)
	}
	if err := run(options{steps, *out, *dbType, *desc, *recordSize, m}); err != nil {
		fail(err)
	}
}

// mode is which of MaxMind's three layouts the records follow.
type mode int

const (
	modeBase mode = iota // the six booleans
	modePlus             // Anonymous Plus, with confidence, last seen, and provider
	modeRisk             // IP Risk, Anonymous Plus with ip_risk
)

// dbType is the default database_type for the mode, our name with the
// MaxMind type it is compatible with.
func (m mode) dbType() string {
	switch m {
	case modePlus:
		return "Open-Anonymous-IP-Plus (compat=GeoIP-Anonymous-Plus)"
	case modeRisk:
		return "Open-IP-Risk (compat=GeoIP2-IP-Risk)"
	}
	return "Open-Anonymous-IP (compat=GeoIP2-Anonymous-IP)"
}

// options is everything run needs, as parsed from the command line.
type options struct {
	steps             []step
	out, dbType, desc string
	recordSize        int
	mode              mode
}

// describe names the build, its mode, and the flags given, in MaxMind's
// field order, such as "open-anonymous-ip dev - tor, hosting, exclude" or
// "open-anonymous-ip dev plus - tor, public_proxy".
func describe(steps []step, m mode) string {
	seen := map[string]bool{}
	for _, s := range steps {
		seen[s.flag] = true
	}
	var used []string
	for _, k := range append(slices.Clone(kinds), kind{"exclude", ""}) {
		if seen[k.flag] {
			used = append(used, k.flag)
		}
	}
	name := "open-anonymous-ip " + version
	switch m {
	case modePlus:
		name += " plus"
	case modeRisk:
		name += " risk"
	}
	return name + " - " + strings.Join(used, ", ")
}

// run applies the steps in order, writes the database, and reads it back
// to prove the first entries of each flagged list carry its flag, unless
// an exclude removed them.
func run(o options) error {
	tree, err := mmdbwriter.New(mmdbwriter.Options{
		DatabaseType: o.dbType,
		Description:  map[string]string{"en": o.desc},
		Languages:    []string{"en"},
		IPVersion:    6,
		RecordSize:   o.recordSize,
		BuildEpoch:   time.Now().Unix(),
	})
	if err != nil {
		return err
	}

	// The first three addresses of each flagged list, checked against the
	// written file. Three, so that an exclude taking the first still
	// leaves the list verified.
	type sample struct {
		addr  netip.Addr
		field string
	}
	var samples []sample
	total := 0
	for _, s := range o.steps {
		insert := func(p netip.Prefix) error {
			samples = slices.DeleteFunc(samples, func(x sample) bool { return p.Contains(x.addr) })
			return tree.InsertPureFunc(p, mmdbtype.Map{}, inserter.Remove)
		}
		label := "excluded"
		if s.field != "" {
			rec, err := record(s.field, s.extras, o.mode)
			if err != nil {
				return fmt.Errorf("%s: %w", s.path, err)
			}
			kept := 0
			insert = func(p netip.Prefix) error {
				if err := tree.InsertPureFunc(p, rec, inserter.TopLevelMerge); err != nil {
					return err
				}
				if kept < 3 {
					samples = append(samples, sample{p.Addr(), s.field})
					kept++
				}
				return nil
			}
			label = s.field
		}
		n, skipped, err := load(s.path, insert)
		if err != nil {
			return err
		}
		fmt.Printf("%-20s %7d entries  %s", label, n, s.path)
		if skipped > 0 {
			fmt.Printf("  (%d reserved skipped)", skipped)
		}
		fmt.Println()
		if s.field != "" {
			total += n
		}
	}
	if total == 0 {
		return errors.New("no lists given, or every list was empty")
	}

	// Every address in no list gets an empty record, as in MaxMind's
	// Anonymous IP and Anonymous Plus test files, so readers that treat a
	// miss as an error never hit one, and an excluded address reads the
	// same as any other ordinary address. Merging an empty map changes no
	// existing record, and the writer skips the reserved and aliased
	// networks by itself. MaxMind's IP Risk files carry no empty record,
	// so that mode does not either.
	if o.mode != modeRisk {
		if err := tree.InsertPureFunc(netip.PrefixFrom(netip.IPv6Unspecified(), 0), mmdbtype.Map{}, inserter.TopLevelMerge); err != nil {
			return fmt.Errorf("inserting the empty default record: %w", err)
		}
	}

	// Write beside the target and rename, so a failure never leaves a
	// partial file at the output path.
	tmp := o.out + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	size, err := tree.WriteTo(f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, o.out)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("writing %s: %w", o.out, err)
	}

	db, err := maxminddb.Open(o.out)
	if err != nil {
		return fmt.Errorf("reopening %s: %w", o.out, err)
	}
	defer func() { _ = db.Close() }()
	for _, s := range samples {
		var flagged bool
		if err := db.Lookup(s.addr).DecodePath(&flagged, s.field); err != nil {
			return fmt.Errorf("verifying %s: %w", s.addr, err)
		}
		if !flagged {
			return fmt.Errorf("verifying %s: %s not set", s.addr, s.field)
		}
	}
	// The empty default record blankets everything, so only the networks
	// carrying a flag are worth counting.
	networks := 0
	for res := range db.Networks() {
		var anon bool
		if err := res.DecodePath(&anon, "is_anonymous"); err != nil {
			return fmt.Errorf("counting networks: %w", err)
		}
		if anon {
			networks++
		}
	}
	fmt.Printf("%s  %d bytes  %d flagged networks  %d nodes  %s\n", o.out, size, networks, db.Metadata.NodeCount, db.Metadata.DatabaseType)
	return nil
}

// load passes every address or network in the file at path to insert and
// returns the count and the number of reserved networks skipped. A file is
// JSON if it starts with { or [, otherwise it is
// one entry per line. Either way an entry is an address or a network in
// CIDR form.
func load(path string, insert func(netip.Prefix) error) (int, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = f.Close() }()
	n, skipped := 0, 0
	add := func(prefix netip.Prefix) error {
		prefix = prefix.Masked()
		err := insert(prefix)
		var reserved *mmdbwriter.ReservedNetworkError
		if *skipReserved && errors.As(err, &reserved) {
			skipped++
			return nil
		}
		if err != nil {
			return err
		}
		n++
		return nil
	}
	br := bufio.NewReader(f)
	if head, _ := br.Peek(3); string(head) == "\ufeff" { // Windows editors write a byte order mark
		_, _ = br.Discard(3)
	}
	head, _ := br.Peek(64)
	if t := bytes.TrimLeft(head, " \t\r\n"); len(t) > 0 && (t[0] == '{' || t[0] == '[') {
		err = loadJSON(br, add)
	} else {
		err = loadLines(br, add)
	}
	if err != nil {
		return 0, 0, fmt.Errorf("%s: %w", path, err)
	}
	if n == 0 {
		return 0, 0, fmt.Errorf("%s: no addresses or networks found", path)
	}
	return n, skipped, nil
}

// loadLines reads one entry per line, followed by a space, tab, comma, or
// # and anything after, which also reads an RFC 8805 geofeed. Blank lines
// and # comments are skipped. Anything else is an error naming the line.
func loadLines(r io.Reader, add func(netip.Prefix) error) error {
	sc := bufio.NewScanner(r)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" || text[0] == '#' {
			continue
		}
		if i := strings.IndexAny(text, " \t,#"); i >= 0 {
			text = text[:i]
		}
		prefix, ok := parse(text)
		if !ok {
			return fmt.Errorf("line %d: %q is not an address or network", line, text)
		}
		if err := add(prefix); err != nil {
			return fmt.Errorf("line %d: %w", line, err)
		}
	}
	return sc.Err()
}

// loadJSON takes every string in the document that is an address or a
// network, wherever it sits, and ignores everything else. That reads the
// published feeds from AWS, Google, Azure, Oracle, Cloudflare, Fastly, and
// GitHub without a schema for each.
func loadJSON(r io.Reader, add func(netip.Prefix) error) error {
	dec := json.NewDecoder(r)
	depth := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			if depth != 0 {
				return io.ErrUnexpectedEOF // a truncated download, not a document
			}
			return nil
		}
		if err != nil {
			return err
		}
		switch tok {
		case json.Delim('{'), json.Delim('['):
			depth++
		case json.Delim('}'), json.Delim(']'):
			depth--
		}
		if s, ok := tok.(string); ok {
			if prefix, ok := parse(s); ok {
				if err := add(prefix); err != nil {
					return fmt.Errorf("%s: %w", s, err)
				}
			}
		}
	}
}

// parse reads a network in CIDR form or a single address as a prefix. An
// IPv4 address written in IPv6 form, ::ffff:1.2.3.4, is stored as IPv4,
// where readers look for it. A zone such as %eth0 is not an address on
// the Internet and is refused.
func parse(s string) (netip.Prefix, bool) {
	prefix, err := netip.ParsePrefix(s)
	if err != nil {
		addr, err := netip.ParseAddr(s)
		if err != nil || addr.Zone() != "" {
			return netip.Prefix{}, false
		}
		prefix = netip.PrefixFrom(addr, addr.BitLen())
	}
	if a := prefix.Addr(); a.Is4In6() && prefix.Bits() >= 96 {
		prefix = netip.PrefixFrom(a.Unmap(), prefix.Bits()-96)
	}
	return prefix, true
}

// record is the value for a list's networks: is_anonymous, the list's
// own flag, and in -plus or -risk mode the extras the list was given.
// Only set fields are written, as in MaxMind's files, and a later list
// sharing a network overrides the fields it sets.
func record(field string, x extras, m mode) (mmdbtype.Map, error) {
	rec := mmdbtype.Map{"is_anonymous": mmdbtype.Bool(true)}
	rec[mmdbtype.String(field)] = mmdbtype.Bool(true)
	if m == modeBase && x != (extras{}) {
		return nil, errors.New("-confidence, -seen, -provider, and -score need -plus")
	}
	if m != modeRisk && x.score != 0 {
		return nil, errors.New("-score needs -risk")
	}
	if x.confidence != 0 {
		rec["anonymizer_confidence"] = mmdbtype.Uint16(x.confidence)
	}
	if x.seen != "" {
		rec["network_last_seen"] = mmdbtype.String(x.seen)
	}
	if x.provider != "" {
		rec["provider_name"] = mmdbtype.String(x.provider)
	}
	if x.score != 0 {
		rec["ip_risk"] = mmdbtype.Float64(x.score)
	}
	return rec, nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "open-anonymous-ip:", err)
	os.Exit(1)
}
