package web

import (
	"net"
	"regexp"
	"strconv"
	"strings"

	"github.com/kjFriday/local-verse-proxy/internal/pyfmt"
)

// SeatPort is the port compose.yaml publishes the proxy on, for the
// seat URL in --docker.
const SeatPort = 4000

// HostRE is HOST_RE, fullmatch: a name or an IPv6 address's text.
var HostRE = regexp.MustCompile(
	`^(?:[A-Za-z0-9.-]{1,253}|[0-9A-Fa-f:]{2,39})\z`)

// Loopback are the names seat_url never offers.
var Loopback = []string{"localhost", "127.0.0.1", "::1"}

func itoa(n int) string { return strconv.Itoa(n) }

// LANIP is this machine's address on its main network: a UDP
// "connect" to 192.0.2.1:9, which sends nothing. "" on error.
func LANIP() string {
	c, err := net.Dial("udp4", "192.0.2.1:9")
	if err != nil {
		return ""
	}
	defer c.Close()
	a, ok := c.LocalAddr().(*net.UDPAddr)
	if !ok {
		return ""
	}
	return a.IP.String()
}

// names is the names this machine goes by (--lan), or in --docker
// only localhost and the given ones: the container's own name means
// nothing to a browser.
func names(docker bool, hostname string, hosts []string) map[string]bool {
	out := map[string]bool{"localhost": true}
	if !docker {
		me := pyfmt.Lower(hostname)
		out[me] = true
		out[me+".local"] = true
		out[strings.SplitN(me, ".", 2)[0]+".local"] = true
	}
	for _, h := range hosts {
		out[pyfmt.Lower(h)] = true
	}
	return out
}

// HostOK blocks DNS rebinding: another site's name pointed at this
// machine. Local: 127.0.0.1, localhost or [::1] with the port only.
// --lan: an IP address, or a name this machine goes by (or one given
// with --host), on the port. --docker: an IP address, localhost, or a
// name listed in FRONTDESK_HOSTS, on any port, since Docker picks it.
func (a *App) HostOK(host string) bool {
	if !a.o.LAN && !a.o.Docker {
		return a.localHosts[host]
	}
	name, port, has, err := pyfmt.URLSplitHost(host)
	if err != nil {
		return false
	}
	if !has || port == 0 {
		port = 80
	}
	if name == "" || (!a.o.Docker && port != a.o.Port) {
		return false
	}
	if a.names[name] {
		return true
	}
	return pyfmt.IsIP(name)
}

// HostName is the name or address this viewer typed, or "" if odd.
func HostName(host string) string {
	name, _, _, err := pyfmt.URLSplitHost(host)
	if err != nil {
		var pe *pyfmt.PortError
		if !asPortError(err, &pe) {
			return ""
		}
	}
	if name != "" && HostRE.MatchString(name) {
		return name
	}
	return ""
}

func asPortError(err error, pe **pyfmt.PortError) bool {
	p, ok := err.(*pyfmt.PortError)
	if ok {
		*pe = p
	}
	return ok
}

// OverNetwork says, with --docker, whether this page came over a
// network: then it warns of plain http. Behind Docker's port mapping
// the address the viewer typed is all there is to go by.
func OverNetwork(host string) bool {
	name := HostName(host)
	if pyfmt.IsIP(name) {
		return !pyfmt.IsLoopback(name)
	}
	return name != "localhost"
}

// SeatURL is the base URL a seat types in: this machine, proxy port.
func (a *App) SeatURL(host string) string {
	if a.o.SeatURL != "" {
		return a.o.SeatURL
	}
	var h string
	var port int
	if a.o.Docker {
		// A container can't see the computer's own address. The
		// proxy is published on the one this viewer typed, so use
		// it; the page says to set FRONTDESK_SEAT_URL if wrong.
		h, port = HostName(host), SeatPort
		if h == "" {
			h = "<proxy-ip>"
		}
	} else {
		if a.o.LAN {
			// The address this viewer used already works on the
			// LAN.
			if name := HostName(host); name != "" &&
				!loopbackName(name) {
				h = name
			}
		}
		if h == "" {
			h = a.o.LANIP()
		}
		if h == "" {
			h = "<proxy-ip>"
		}
		port = a.o.Live.Get().Port
	}
	if strings.Contains(h, ":") {
		h = "[" + h + "]"
	}
	return "http://" + h + ":" + itoa(port)
}

func loopbackName(n string) bool {
	for _, l := range Loopback {
		if n == l {
			return true
		}
	}
	return false
}
