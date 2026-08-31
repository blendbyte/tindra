package alerts

import (
	"context"
	"fmt"
	"net"
	"net/url"
)

// ValidateWebhookURL checks that urlStr is a safe HTTP/HTTPS endpoint.
// When allowPrivate is false (default), loopback, link-local, and RFC-1918
// addresses are blocked to prevent SSRF. Set WEBHOOK_ALLOW_PRIVATE_IPS=true
// to permit internal hosts for self-hosted deployments.
func ValidateWebhookURL(ctx context.Context, urlStr string, allowPrivate bool) error {
	u, err := url.Parse(urlStr)
	if err != nil {
		return fmt.Errorf("invalid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("webhook URL must use http or https")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("webhook URL missing host")
	}
	if allowPrivate {
		return nil
	}
	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil {
		// Can't resolve → block; don't let unresolvable hosts slip through.
		return fmt.Errorf("webhook URL host could not be resolved")
	}
	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		if ip == nil {
			continue
		}
		if isBlockedIP(ip) {
			return fmt.Errorf("webhook URL must not resolve to a private or internal address")
		}
	}
	return nil
}

// isBlockedIP reports whether ip is a loopback, link-local, private, or
// unspecified address, including IPv4 values embedded in IPv6 transition
// forms (NAT64, 6to4, Teredo, IPv4-compatible). IPv4-mapped addresses are
// already classified by Go's To4() path inside the net.IP helpers.
func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if blockedDirect(ip) {
		return true
	}
	for _, embedded := range embeddedIPv4(ip) {
		if blockedDirect(embedded) {
			return true
		}
	}
	return false
}

func blockedDirect(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsPrivate() || ip.IsUnspecified()
}

func embeddedIPv4(ip net.IP) []net.IP {
	v6 := ip.To16()
	if v6 == nil {
		return nil
	}
	// Pure IPv4 and IPv4-mapped (::ffff:x.x.x.x) are classified via To4().
	if ip.To4() != nil {
		return nil
	}

	var out []net.IP

	// Well-known NAT64 prefix 64:ff9b::/96 (RFC 6052). The prefix itself is
	// globally scoped, so a destination like 64:ff9b::169.254.169.254 passes
	// IsPrivate/IsLoopback while a NAT64 gateway translates it to link-local.
	if v6[0] == 0x00 && v6[1] == 0x64 && v6[2] == 0xff && v6[3] == 0x9b &&
		v6[4] == 0 && v6[5] == 0 && v6[6] == 0 && v6[7] == 0 &&
		v6[8] == 0 && v6[9] == 0 && v6[10] == 0 && v6[11] == 0 {
		out = append(out, net.IPv4(v6[12], v6[13], v6[14], v6[15]))
	}

	// 6to4 2002::/16: IPv4 is bytes 2-5.
	if v6[0] == 0x20 && v6[1] == 0x02 {
		out = append(out, net.IPv4(v6[2], v6[3], v6[4], v6[5]))
	}

	// Teredo 2001:0000::/32: server IPv4 at bytes 4-7, client IPv4 at the
	// last 4 bytes XOR 0xff (RFC 4380).
	if v6[0] == 0x20 && v6[1] == 0x01 && v6[2] == 0x00 && v6[3] == 0x00 {
		out = append(out, net.IPv4(v6[4], v6[5], v6[6], v6[7]))
		out = append(out, net.IPv4(v6[12]^0xff, v6[13]^0xff, v6[14]^0xff, v6[15]^0xff))
	}

	if isIPv4Compatible(v6) {
		out = append(out, net.IPv4(v6[12], v6[13], v6[14], v6[15]))
	}

	return out
}

func isZeros(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

// isIPv4Compatible reports the deprecated RFC 4291 ::x.x.x.x form.
// :: and ::1 are excluded; they are unspecified and loopback, not transition addresses.
func isIPv4Compatible(v6 net.IP) bool {
	if !isZeros(v6[:12]) {
		return false
	}
	if v6[12] == 0 && v6[13] == 0 && v6[14] == 0 && (v6[15] == 0 || v6[15] == 1) {
		return false
	}
	return true
}
