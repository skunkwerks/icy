package ssrf

import (
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// AllowedNetworks is a parsed list of IP prefixes that outbound requests may
// reach even though they fall inside a blocked range. Build one with
// ParseAllowedNetworks. The zero value allows nothing extra.
//
// Only addresses are listed, never hostnames. A hostname entry would let
// anyone who controls a DNS record point it at an internal host, so the
// check is always made on the address that is actually dialled.
type AllowedNetworks []netip.Prefix

// ParseAllowedNetworks parses allowlist entries. Each is a CIDR prefix or a
// single IP address:
//
//	10.0.0.0/8, 192.168.1.20, fd00::/8
//
// It fails on the first entry it cannot read, so a typo in the config is
// reported when the config loads instead of silently matching nothing.
func ParseAllowedNetworks(entries []string) (AllowedNetworks, error) {
	nets := make(AllowedNetworks, 0, len(entries))
	for _, raw := range entries {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			return nil, fmt.Errorf("allowed network %q: empty entry", raw)
		}

		if strings.Contains(entry, "/") {
			p, err := netip.ParsePrefix(entry)
			if err != nil {
				return nil, fmt.Errorf("allowed network %q: invalid network", raw)
			}
			// An IPv4-mapped prefix (::ffff:10.0.0.0/104) names IPv4
			// addresses, and those are matched unmapped below.
			if p.Addr().Is4In6() {
				bits := p.Bits() - 96
				if bits < 0 {
					return nil, fmt.Errorf("allowed network %q: invalid network", raw)
				}
				p = netip.PrefixFrom(p.Addr().Unmap(), bits)
			}
			nets = append(nets, p.Masked())
			continue
		}

		addr, err := netip.ParseAddr(entry)
		if err != nil {
			return nil, fmt.Errorf("allowed network %q: not an IP address or network", raw)
		}
		addr = addr.Unmap()
		nets = append(nets, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return nets, nil
}

// Contains reports whether ip falls inside one of the allowed networks. An
// IPv4 address written in IPv6 form is matched as the IPv4 address it names.
// An address that cannot be parsed is never contained.
func (a AllowedNetworks) Contains(ip net.IP) bool {
	if len(a) == 0 {
		return false
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	addr = addr.Unmap()
	for _, p := range a {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// blocked reports whether a request to ip must be refused: it is private or
// internal and not covered by the allowlist.
func (a AllowedNetworks) blocked(ip net.IP) bool {
	return isPrivateOrInternal(ip) && !a.Contains(ip)
}
