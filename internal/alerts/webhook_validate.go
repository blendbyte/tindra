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
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsPrivate() || ip.IsUnspecified() {
			return fmt.Errorf("webhook URL must not resolve to a private or internal address")
		}
	}
	return nil
}
