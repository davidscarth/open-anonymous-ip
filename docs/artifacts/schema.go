// SPDX-License-Identifier: Apache-2.0

//go:build schema

// schema reports what is inside MMDB files: the metadata, each field with
// its wire type, how many networks carry it, and its values or range, the
// fields grouped by flag combination, and whether the three IPv4 aliases
// find the same record.
//
//	go run -tags schema schema.go -o schema.txt file.mmdb ...
//	go run -tags schema schema.go -o schema.txt testdata/
//
// A directory argument checks every *.mmdb in it. A file that does not
// read is reported and skipped. A run over more than one file ends with
// a summary across them all. -o writes the report to a UTF-8 file,
// without it the report goes to stdout.
package main

import (
	"flag"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/oschwald/maxminddb-golang/v2"
	"github.com/oschwald/maxminddb-golang/v2/mmdbdata"
)

// kindProbe records the wire kind of each top-level field, resolved past
// pointers, which a plain decode cannot see: every unsigned size decodes
// to uint64.
type kindProbe map[string]string

func (p kindProbe) UnmarshalMaxMindDB(d *mmdbdata.Decoder) error {
	entries, _, err := d.ReadMap()
	if err != nil {
		return err
	}
	for key, err := range entries {
		if err != nil {
			return err
		}
		k, err := d.PeekKind()
		if err != nil {
			return err
		}
		p[string(key)] = k.String()
		if err := d.SkipValue(); err != nil {
			return err
		}
	}
	return nil
}

var dateForm = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// stat accumulates one field's networks, wire kinds, distinct values up to
// a cap, numeric range, and whether every string value is a YYYY-MM-DD date.
type stat struct {
	n        int
	kinds    map[string]int
	values   map[string]bool
	more     bool // values overflowed the cap
	min, max float64
	numeric  bool
	dates    bool
}

func (s *stat) add(kind string, v any) {
	if s.kinds == nil {
		s.kinds = map[string]int{}
		s.values = map[string]bool{}
		s.dates = true
	}
	s.n++
	s.kinds[kind]++
	text := fmt.Sprint(v)
	if len(s.values) < 9 {
		s.values[text] = true
	} else if !s.values[text] {
		s.more = true
	}
	var f float64
	switch x := v.(type) {
	case uint64:
		f = float64(x)
	case int32:
		f = float64(x)
	case float64:
		f = x
	case float32:
		f = float64(x)
	case string:
		if !dateForm.MatchString(x) {
			s.dates = false
		}
		return
	default:
		s.dates = false
		return
	}
	s.dates = false
	if s.n == 1 || !s.numeric {
		s.min, s.max = f, f
		s.numeric = true
		return
	}
	s.min, s.max = min(s.min, f), max(s.max, f)
}

// describe is the values list when it is small, the numeric range when it
// is not, and a note when every string is a date.
func (s *stat) describe() string {
	if !s.more {
		var vs []string
		for v := range s.values {
			vs = append(vs, v)
		}
		sort.Strings(vs)
		return fmt.Sprintf("  values %v", vs)
	}
	if s.numeric {
		return fmt.Sprintf("  range [%s .. %s]",
			strconv.FormatFloat(s.min, 'g', -1, 64), strconv.FormatFloat(s.max, 'g', -1, 64))
	}
	if s.dates {
		return "  all YYYY-MM-DD"
	}
	return ""
}

func (s *stat) kindList() string {
	var ks []string
	for k := range s.kinds {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	out := ""
	for i, k := range ks {
		if i > 0 {
			out += "/"
		}
		out += k
	}
	return out
}

func main() {
	out := flag.String("o", "", "write the report to this file as UTF-8 instead of stdout")
	flag.Parse()
	var w io.Writer = os.Stdout
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer func() { _ = f.Close() }()
		w = f
	}
	var paths []string
	for _, arg := range flag.Args() {
		info, err := os.Stat(arg)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			continue
		}
		if !info.IsDir() {
			paths = append(paths, arg)
			continue
		}
		found, err := filepath.Glob(filepath.Join(arg, "*.mmdb"))
		if err != nil || len(found) == 0 {
			fmt.Fprintf(os.Stderr, "%s: no *.mmdb files\n", arg)
			continue
		}
		sort.Strings(found)
		paths = append(paths, found...)
	}

	// Across every file scanned, for the closing summary.
	fieldFiles := map[string]map[string]bool{} // field -> file set
	fieldKinds := map[string]map[string]bool{} // field -> kind set
	shapeFiles := map[string]map[string]bool{} // shape -> file set
	shapeNets := map[string]int{}              // shape -> networks in all files

	for _, path := range paths {
		db, err := maxminddb.Open(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			fmt.Fprintf(w, "%s\n  unreadable     %v\n", filepath.Base(path), err)
			continue
		}
		m := db.Metadata
		fmt.Fprintf(w, "%s\n", filepath.Base(path))
		fmt.Fprintf(w, "  database_type  %s\n", m.DatabaseType)
		fmt.Fprintf(w, "  description    %v\n", m.Description)
		fmt.Fprintf(w, "  languages      %v\n", m.Languages)
		fmt.Fprintf(w, "  binary_format  %d.%d\n", m.BinaryFormatMajorVersion, m.BinaryFormatMinorVersion)
		fmt.Fprintf(w, "  ip_version     %d\n", m.IPVersion)
		fmt.Fprintf(w, "  record_size    %d\n", m.RecordSize)
		fmt.Fprintf(w, "  node_count     %d\n", m.NodeCount)
		fmt.Fprintf(w, "  build_epoch    %d (%s)\n", m.BuildEpoch, time.Unix(int64(m.BuildEpoch), 0).UTC().Format("2006-01-02"))

		fields := map[string]*stat{}            // field -> stats over the whole file
		shapes := map[string]int{}              // flag combination -> networks
		extras := map[string]map[string]*stat{} // flag combination -> non-flag field -> stats
		networks, empty := 0, 0
		var testAddr netip.Addr // an IPv4 with a non-empty record, for the alias check
		var testRec string
		for res := range db.Networks() {
			networks++
			var rec map[string]any
			if err := res.Decode(&rec); err != nil {
				fmt.Fprintln(os.Stderr, res.Prefix(), err)
				continue
			}
			probe := kindProbe{}
			if err := res.Decode(&probe); err != nil {
				fmt.Fprintln(os.Stderr, res.Prefix(), "kinds:", err)
			}
			if len(rec) == 0 {
				empty++
				continue
			}
			if a := res.Prefix().Addr().Unmap(); !testAddr.IsValid() && a.Is4() {
				testAddr, testRec = a, fmt.Sprint(rec)
			}
			var flagNames []string
			for k, v := range rec {
				fs := fields[k]
				if fs == nil {
					fs = &stat{}
					fields[k] = fs
				}
				fs.add(probe[k], v)
				if b, ok := v.(bool); ok && b && len(k) > 3 && k[:3] == "is_" {
					flagNames = append(flagNames, k[3:])
				}
			}
			sort.Strings(flagNames)
			shape := "(no flags)"
			if len(flagNames) > 0 {
				shape = flagNames[0]
				for _, f := range flagNames[1:] {
					shape += "+" + f
				}
			}
			shapes[shape]++
			for k, v := range rec {
				if b, ok := v.(bool); ok && b && len(k) > 3 && k[:3] == "is_" {
					continue
				}
				if extras[shape] == nil {
					extras[shape] = map[string]*stat{}
				}
				es := extras[shape][k]
				if es == nil {
					es = &stat{}
					extras[shape][k] = es
				}
				es.add(probe[k], v)
			}
		}
		fmt.Fprintf(w, "  networks       %d, %d with an empty record\n", networks, empty)

		// The aliases MaxMind's files carry: the same IPv4, looked up as
		// itself at an IPv4-mapped, 6to4, and Teredo address, finds the
		// same record when the alias is in the tree.
		switch {
		case m.IPVersion != 6:
			fmt.Fprintf(w, "  aliases        not applicable, ip_version %d\n", m.IPVersion)
		case !testAddr.IsValid():
			fmt.Fprintf(w, "  aliases        no IPv4 record to test\n")
		default:
			b4 := testAddr.As4()
			same := func(a netip.Addr) string {
				var rec map[string]any
				res := db.Lookup(a)
				if err := res.Decode(&rec); err != nil || !res.Found() || fmt.Sprint(rec) != testRec {
					return "no"
				}
				return "yes"
			}
			var sixto4, teredo [16]byte
			sixto4[0], sixto4[1] = 0x20, 0x02
			copy(sixto4[2:6], b4[:])
			teredo[0], teredo[1] = 0x20, 0x01
			copy(teredo[4:8], b4[:])
			fmt.Fprintf(w, "  aliases        v4mapped %s, 6to4 %s, teredo %s  (tested with %s)\n",
				same(netip.AddrFrom16(testAddr.As16())),
				same(netip.AddrFrom16(sixto4)),
				same(netip.AddrFrom16(teredo)),
				testAddr)
		}

		var names []string
		for k := range fields {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			s := fields[k]
			fmt.Fprintf(w, "  %-24s %-10s %6d networks%s\n", k, s.kindList(), s.n, s.describe())
			if fieldFiles[k] == nil {
				fieldFiles[k] = map[string]bool{}
				fieldKinds[k] = map[string]bool{}
			}
			fieldFiles[k][path] = true
			for kind := range s.kinds {
				fieldKinds[k][kind] = true
			}
		}

		// The fields each flag combination carries, which the per-field
		// summary cannot show: a file-wide count says confidence exists,
		// this says which kinds of record it sits on.
		var shapeNames []string
		for k := range shapes {
			shapeNames = append(shapeNames, k)
		}
		sort.Strings(shapeNames)
		fmt.Fprintf(w, "  shapes:\n")
		for _, shape := range shapeNames {
			fmt.Fprintf(w, "    %-42s %5d networks\n", shape, shapes[shape])
			if shapeFiles[shape] == nil {
				shapeFiles[shape] = map[string]bool{}
			}
			shapeFiles[shape][path] = true
			shapeNets[shape] += shapes[shape]
			var ks []string
			for k := range extras[shape] {
				ks = append(ks, k)
			}
			sort.Strings(ks)
			for _, k := range ks {
				s := extras[shape][k]
				fmt.Fprintf(w, "      %-24s %-10s %4d%s\n", k, s.kindList(), s.n, s.describe())
			}
		}
		_ = db.Close()
	}

	// The rollup: which files carry each field and each flag combination,
	// a histogram of the whole set.
	if len(paths) < 2 {
		return
	}
	fmt.Fprintf(w, "summary of %d files\n", len(paths))
	var names []string
	for k := range fieldFiles {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		var ks []string
		for kind := range fieldKinds[k] {
			ks = append(ks, kind)
		}
		sort.Strings(ks)
		kinds := ""
		for i, kind := range ks {
			if i > 0 {
				kinds += "/"
			}
			kinds += kind
		}
		fmt.Fprintf(w, "  %-32s %3d files  %s\n", k, len(fieldFiles[k]), kinds)
	}
	names = names[:0]
	for shape := range shapeFiles {
		names = append(names, shape)
	}
	sort.Strings(names)
	fmt.Fprintf(w, "  shapes:\n")
	for _, shape := range names {
		fmt.Fprintf(w, "    %-42s %3d files  %6d networks\n", shape, len(shapeFiles[shape]), shapeNets[shape])
	}
}
