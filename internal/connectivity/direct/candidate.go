package direct

import (
	"net"
	"sort"
	"strconv"
)

const DefaultPrivateCandidateLimit = 8

type CandidateSet struct {
	PublicUDPAddr   string   `json:"public_udp_addr,omitempty"`
	PrivateUDPAddrs []string `json:"private_udp_addrs,omitempty"`
}

type PrivateAddressOptions struct {
	Limit             int
	AllowLoopback     bool
	AllowTestNetworks bool
}

func NewCandidateSet(public *net.UDPAddr, private []*net.UDPAddr, opts PrivateAddressOptions) CandidateSet {
	candidates := CandidateSet{
		PrivateUDPAddrs: SanitizePrivateUDPAddrs(private, opts),
	}
	if public != nil {
		candidates.PublicUDPAddr = NormalizeUDPAddr(public)
	}
	return candidates
}

func SanitizePrivateUDPAddrs(addrs []*net.UDPAddr, opts PrivateAddressOptions) []string {
	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultPrivateCandidateLimit
	}

	seen := make(map[string]struct{})
	for _, addr := range addrs {
		if addr == nil || !allowedPrivateIP(addr.IP, opts) {
			continue
		}
		normalized := NormalizeUDPAddr(addr)
		if normalized == "" {
			continue
		}
		seen[normalized] = struct{}{}
	}

	out := make([]string, 0, len(seen))
	for addr := range seen {
		out = append(out, addr)
	}
	sort.Strings(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func NormalizeUDPAddr(addr *net.UDPAddr) string {
	if addr == nil || addr.IP == nil || addr.Port <= 0 || addr.Port > 65535 {
		return ""
	}
	return net.JoinHostPort(addr.IP.String(), portString(addr.Port))
}

func CollectPrivateUDPAddrs(port int, opts PrivateAddressOptions) ([]string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var addrs []*net.UDPAddr
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		ifaceAddrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, ifaceAddr := range ifaceAddrs {
			ip, ok := ipFromAddr(ifaceAddr)
			if !ok {
				continue
			}
			addrs = append(addrs, &net.UDPAddr{IP: ip, Port: port})
		}
	}
	return SanitizePrivateUDPAddrs(addrs, opts), nil
}

func allowedPrivateIP(ip net.IP, opts PrivateAddressOptions) bool {
	if ip == nil {
		return false
	}
	if ip.IsPrivate() {
		return true
	}
	if ip.IsLinkLocalUnicast() {
		return true
	}
	if ip.IsLoopback() {
		return opts.AllowLoopback
	}
	if opts.AllowTestNetworks && isDocumentationIP(ip) {
		return true
	}
	return false
}

func isDocumentationIP(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	switch {
	case ip4[0] == 192 && ip4[1] == 0 && ip4[2] == 2:
		return true
	case ip4[0] == 198 && ip4[1] == 51 && ip4[2] == 100:
		return true
	case ip4[0] == 203 && ip4[1] == 0 && ip4[2] == 113:
		return true
	default:
		return false
	}
}

func ipFromAddr(addr net.Addr) (net.IP, bool) {
	switch v := addr.(type) {
	case *net.IPNet:
		return v.IP, true
	case *net.IPAddr:
		return v.IP, true
	default:
		return nil, false
	}
}

func portString(port int) string {
	return strconv.Itoa(port)
}
