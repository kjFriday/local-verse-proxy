package pyfmt

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// PortError is urlsplit's .port refusing: the name is still good.
type PortError struct{ Msg string }

func (e *PortError) Error() string { return e.Msg }

// URLSplitHost is urlsplit("//" + host): hostname (lowercased, no
// brackets, userinfo cut), port, and Python's ValueError cases. When
// only the port is bad, err is a *PortError and name is still set
// (Python's .hostname works then; .port raises).
func URLSplitHost(host string) (name string, port int,
	hasPort bool, err error) {
	url := "//" + host
	for _, c := range []string{"\t", "\r", "\n"} {
		url = strings.ReplaceAll(url, c, "")
	}
	netloc := url[2:]
	if k := strings.IndexAny(netloc, "/?#"); k >= 0 {
		netloc = netloc[:k]
	}
	open := strings.Contains(netloc, "[")
	shut := strings.Contains(netloc, "]")
	if open != shut {
		return "", 0, false, errors.New("Invalid IPv6 URL")
	}
	if open && shut {
		if err := checkBracketed(netloc); err != nil {
			return "", 0, false, err
		}
	}
	if err := checkNetloc(netloc); err != nil {
		return "", 0, false, err
	}
	hostinfo := netloc
	if k := strings.LastIndexByte(netloc, '@'); k >= 0 {
		hostinfo = netloc[k+1:]
	}
	var hn, ps string
	if k := strings.IndexByte(hostinfo, '['); k >= 0 {
		br := hostinfo[k+1:]
		hn, ps, _ = strings.Cut(br, "]")
		_, ps, _ = strings.Cut(ps, ":")
	} else {
		hn, ps, _ = strings.Cut(hostinfo, ":")
	}
	if hn != "" {
		h, pct, zone := hn, "", ""
		if k := strings.IndexByte(hn, '%'); k >= 0 {
			h, pct, zone = hn[:k], "%", hn[k+1:]
		}
		name = Lower(h) + pct + zone
	}
	if ps == "" {
		return name, 0, false, nil
	}
	if !asciiDigits(ps) {
		return name, 0, false, &PortError{"Port could not be cast " +
			"to integer value as " + ReprString(ps)}
	}
	n, err := strconv.Atoi(strings.TrimLeft(ps, "0") + "0")
	if err != nil || len(strings.TrimLeft(ps, "0")) > 5 {
		return name, 0, false, &PortError{"Port out of range 0-65535"}
	}
	n /= 10
	if n > 65535 {
		return name, 0, false, &PortError{"Port out of range 0-65535"}
	}
	return name, n, true, nil
}

func asciiDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isDigit(s[i]) {
			return false
		}
	}
	return true
}

var ipvFuture = regexp.MustCompile(`^[vV][a-fA-F0-9]+\.(?s:.+)$`)

// checkBracketed is _check_bracketed_netloc.
func checkBracketed(netloc string) error {
	hp := netloc
	if k := strings.LastIndexByte(netloc, '@'); k >= 0 {
		hp = netloc[k+1:]
	}
	var hn string
	before, br, has := strings.Cut(hp, "[")
	if has {
		if before != "" {
			return errors.New("Invalid IPv6 URL")
		}
		var ps string
		hn, ps, _ = strings.Cut(br, "]")
		if ps != "" && !strings.HasPrefix(ps, ":") {
			return errors.New("Invalid IPv6 URL")
		}
	} else {
		hn, _, _ = strings.Cut(hp, ":")
	}
	if strings.HasPrefix(hn, "v") || strings.HasPrefix(hn, "V") {
		if !ipvFuture.MatchString(hn) {
			return errors.New("IPvFuture address is invalid")
		}
		return nil
	}
	if is4(hn) {
		return errors.New("An IPv4 address cannot be in brackets")
	}
	if _, ok := parse6(hn); !ok {
		return errors.New(ReprString(hn) + " does not appear to be " +
			"an IPv4 or IPv6 address")
	}
	return nil
}

// checkNetloc is _checknetloc: characters that NFKC turns into URL
// punctuation. These 19 are every such character in Unicode 15.1
// (Python 3.13's tables), so no normalizer is needed.
func checkNetloc(netloc string) error {
	if netloc == "" || isASCII(netloc) {
		return nil
	}
	for _, r := range netloc {
		if strings.ContainsRune(nfkcPunct, r) {
			return errors.New("netloc '" + netloc + "' contains " +
				"invalid characters under NFKC normalization")
		}
	}
	return nil
}

const nfkcPunct = "\u2047\u2048\u2049\u2100\u2101\u2105\u2106" +
	"\u2a74\ufe13\ufe16\ufe55\ufe56\ufe5f\ufe6b\uff03\uff0f" +
	"\uff1a\uff1f\uff20"

// IsIP is ipaddress.ip_address(s) accepting s.
func IsIP(s string) bool {
	if is4(s) {
		return true
	}
	_, ok := parse6(s)
	return ok
}

// IsLoopback is ip_address(s).is_loopback: 127.0.0.0/8, ::1, and (as
// Python 3.13) an IPv4-mapped loopback address. Not an address:
// false.
func IsLoopback(s string) bool {
	if v, ok := parse4(s); ok {
		return v>>24 == 127
	}
	b, ok := parse6(s)
	if !ok {
		return false
	}
	mapped := true
	for i := 0; i < 10; i++ {
		if b[i] != 0 {
			mapped = false
		}
	}
	if mapped && b[10] == 0xff && b[11] == 0xff {
		return b[12] == 127
	}
	for i := 0; i < 15; i++ {
		if b[i] != 0 {
			return false
		}
	}
	return b[15] == 1
}

func is4(s string) bool {
	_, ok := parse4(s)
	return ok
}

// parse4 is IPv4Address's strict dotted quad: four decimal parts of
// one to three ASCII digits, no leading zeros, each up to 255.
func parse4(s string) (uint32, bool) {
	if s == "" || strings.Contains(s, "/") {
		return 0, false
	}
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return 0, false
	}
	var v uint32
	for _, p := range parts {
		if p == "" || len(p) > 3 || !asciiDigits(p) ||
			p != "0" && p[0] == '0' {
			return 0, false
		}
		n, _ := strconv.Atoi(p)
		if n > 255 {
			return 0, false
		}
		v = v<<8 | uint32(n)
	}
	return v, true
}

// parse6 is IPv6Address's parser, scope id included.
func parse6(s string) ([16]byte, bool) {
	var out [16]byte
	if s == "" || strings.Contains(s, "/") {
		return out, false
	}
	addr := s
	if k := strings.IndexByte(s, '%'); k >= 0 {
		addr = s[:k]
		scope := s[k+1:]
		if scope == "" || strings.Contains(scope, "%") {
			return out, false
		}
	}
	parts := strings.Split(addr, ":")
	if len(parts) < 3 {
		return out, false
	}
	if last := parts[len(parts)-1]; strings.Contains(last, ".") {
		v, ok := parse4(last)
		if !ok {
			return out, false
		}
		parts = append(parts[:len(parts)-1],
			strconv.FormatUint(uint64(v>>16), 16),
			strconv.FormatUint(uint64(v&0xffff), 16))
	}
	if len(parts) > 9 {
		return out, false
	}
	skip := -1
	for i := 1; i < len(parts)-1; i++ {
		if parts[i] == "" {
			if skip >= 0 {
				return out, false
			}
			skip = i
		}
	}
	var hi, lo, skipped int
	if skip >= 0 {
		hi, lo = skip, len(parts)-skip-1
		if parts[0] == "" {
			hi--
			if hi != 0 {
				return out, false
			}
		}
		if parts[len(parts)-1] == "" {
			lo--
			if lo != 0 {
				return out, false
			}
		}
		skipped = 8 - (hi + lo)
		if skipped < 1 {
			return out, false
		}
	} else {
		if len(parts) != 8 || parts[0] == "" ||
			parts[len(parts)-1] == "" {
			return out, false
		}
		hi, lo = len(parts), 0
	}
	var words []uint16
	for i := 0; i < hi; i++ {
		w, ok := hextet(parts[i])
		if !ok {
			return out, false
		}
		words = append(words, w)
	}
	for i := 0; i < skipped; i++ {
		words = append(words, 0)
	}
	for i := len(parts) - lo; i < len(parts); i++ {
		w, ok := hextet(parts[i])
		if !ok {
			return out, false
		}
		words = append(words, w)
	}
	if len(words) != 8 {
		return out, false
	}
	for i, w := range words {
		out[2*i], out[2*i+1] = byte(w>>8), byte(w)
	}
	return out, true
}

func hextet(p string) (uint16, bool) {
	if p == "" || len(p) > 4 {
		return 0, false
	}
	for _, c := range p {
		if c > unicode.MaxASCII || !strings.ContainsRune(
			"0123456789abcdefABCDEF", c) {
			return 0, false
		}
	}
	v, err := strconv.ParseUint(p, 16, 16)
	return uint16(v), err == nil
}
