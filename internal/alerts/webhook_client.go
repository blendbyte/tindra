package alerts

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/blendbyte/tindra/internal/version"
)

// NewWebhookClient uses the protected outbound transport with the alert user agent.
func NewWebhookClient(allowPrivate bool) *http.Client {
	client := NewOutboundClient(allowPrivate)
	client.Transport = userAgentTransport{rt: client.Transport}
	return client
}

// NewOutboundClient checks every resolved destination immediately before dialing
// and connects to the checked IP. Redirects use the same protected transport.
// Callers can customize timeouts and redirect policy without losing protection.
func NewOutboundClient(allowPrivate bool) *http.Client {
	dialer := &net.Dialer{Timeout: 30 * time.Second}
	transport := newOutboundTransport(allowPrivate, net.DefaultResolver.LookupHost, dialer.DialContext)
	return &http.Client{Timeout: 10 * time.Second, Transport: transport}
}

// Keep resolution and dialing separate so the checked address is the only
// address passed to the connector, including after DNS changes.
func newOutboundTransport(allowPrivate bool,
	lookup func(context.Context, string) ([]string, error),
	dial func(context.Context, string, string) (net.Conn, error),
) *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := lookup(ctx, host)
			if err != nil {
				return nil, fmt.Errorf("could not resolve %s", host)
			}
			if len(ips) == 0 {
				return nil, fmt.Errorf("no addresses for %s", host)
			}
			for _, ipStr := range ips {
				ip := net.ParseIP(ipStr)
				if ip == nil {
					return nil, fmt.Errorf("destination host %s resolved to an invalid address", host)
				}
				if !allowPrivate && isBlockedIP(ip) {
					return nil, fmt.Errorf("destination host %s resolves to a private address", host)
				}
			}
			// Dial the first checked IP directly - no second DNS lookup.
			return dial(ctx, network, net.JoinHostPort(ips[0], port))
		},
	}
}

type userAgentTransport struct {
	rt http.RoundTripper
}

func (t userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("User-Agent", "Tindra/"+version.App+" (+https://tindra.sh)")
	return t.rt.RoundTrip(req)
}
