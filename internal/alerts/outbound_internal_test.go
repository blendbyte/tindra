package alerts

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOutboundTransportPinsValidatedAddresses(t *testing.T) {
	for _, tc := range []struct {
		name         string
		addr         string
		ips          []string
		lookupErr    error
		allowPrivate bool
		wantError    string
		wantDial     string
	}{
		{name: "malformed destination", addr: "missing-port", wantError: "missing port"},
		{name: "DNS failure", addr: "public.example:443", lookupErr: errors.New("DNS failed"), wantError: "could not resolve"},
		{name: "empty DNS answer", addr: "public.example:443", wantError: "no addresses"},
		{name: "invalid DNS address", addr: "public.example:443", ips: []string{"another.example"}, wantError: "invalid address"},
		{name: "invalid DNS with private opt-in", addr: "public.example:443", ips: []string{"another.example"}, allowPrivate: true, wantError: "invalid address"},
		{name: "private address", addr: "public.example:443", ips: []string{"127.0.0.1"}, wantError: "private address"},
		{name: "mixed DNS answer", addr: "public.example:443", ips: []string{"203.0.113.5", "10.0.0.1"}, wantError: "private address"},
		{name: "malformed secondary answer", addr: "public.example:443", ips: []string{"203.0.113.5", "invalid"}, wantError: "invalid address"},
		{name: "public IPv4 pinned", addr: "public.example:443", ips: []string{"203.0.113.5", "203.0.113.6"}, wantDial: "203.0.113.5:443"},
		{name: "public IPv6 pinned", addr: "public.example:443", ips: []string{"2001:db8::1"}, wantDial: "[2001:db8::1]:443"},
		{name: "explicit private opt-in", addr: "public.example:443", ips: []string{"127.0.0.1"}, allowPrivate: true, wantDial: "127.0.0.1:443"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lookups, dials := 0, 0
			dialErr := errors.New("fixture dial result")
			transport := newOutboundTransport(tc.allowPrivate,
				func(ctx context.Context, host string) ([]string, error) {
					lookups++
					require.Equal(t, "public.example", host)
					// A second lookup would simulate DNS rebinding to a private address.
					if lookups > 1 {
						return []string{"127.0.0.1"}, nil
					}
					return tc.ips, tc.lookupErr
				},
				func(ctx context.Context, network, addr string) (net.Conn, error) {
					dials++
					require.Equal(t, "tcp", network)
					require.Equal(t, tc.wantDial, addr)
					return nil, dialErr
				})
			conn, err := transport.DialContext(t.Context(), "tcp", tc.addr)
			require.Nil(t, conn)
			if tc.wantDial != "" {
				require.ErrorIs(t, err, dialErr)
				require.Equal(t, 1, dials)
			} else {
				require.ErrorContains(t, err, tc.wantError)
				require.Zero(t, dials)
			}
			expectedLookups := 1
			if tc.name == "malformed destination" {
				expectedLookups = 0
			}
			require.Equal(t, expectedLookups, lookups)
		})
	}
}
