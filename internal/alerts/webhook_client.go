package alerts

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/blendbyte/tindra/internal/version"
)

// newWebhookClient returns an HTTP client whose DialContext validates the
// resolved IP against the private-address block list immediately before each
// TCP connection. Pinning the dial to the checked IP eliminates the TOCTOU
// window between ValidateWebhookURL (run at save time) and actual delivery.
// NewWebhookClient builds an HTTP client whose DialContext validates resolved
// IPs at dial time. Exported so the ingest passthrough can reuse the same
// safe dialer without duplicating the logic.
func NewWebhookClient(allowPrivate bool) *http.Client {
	dialer := &net.Dialer{Timeout: 30 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupHost(ctx, host)
			if err != nil {
				return nil, fmt.Errorf("could not resolve %s", host)
			}
			if len(ips) == 0 {
				return nil, fmt.Errorf("no addresses for %s", host)
			}
			if !allowPrivate {
				for _, ipStr := range ips {
					ip := net.ParseIP(ipStr)
					if ip == nil {
						continue
					}
					if isBlockedIP(ip) {
						return nil, fmt.Errorf("webhook host %s resolves to a private address", host)
					}
				}
			}
			// Dial the first checked IP directly - no second DNS lookup.
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0], port))
		},
	}
	return &http.Client{Timeout: 10 * time.Second, Transport: userAgentTransport{rt: transport}}
}

type userAgentTransport struct {
	rt http.RoundTripper
}

func (t userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("User-Agent", "Tindra/"+version.App+" (+https://tindra.sh)")
	return t.rt.RoundTrip(req)
}
