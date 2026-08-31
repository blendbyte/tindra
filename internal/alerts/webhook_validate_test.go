package alerts_test

import (
	"context"
	"testing"

	"github.com/blendbyte/tindra/internal/alerts"
)

func TestValidateWebhookURL(t *testing.T) {
	tests := []struct {
		name         string
		url          string
		allowPrivate bool
		wantErr      bool
	}{
		// Use bare IPs so net.LookupHost resolves without external DNS in CI.
		{"valid https", "https://203.0.113.1/notify", false, false},
		{"valid http", "http://203.0.113.1/notify", false, false},
		{"bad scheme ftp", "ftp://hooks.example.com/notify", false, true},
		{"bad scheme file", "file:///etc/passwd", false, true},
		{"empty url", "", false, true},
		{"loopback blocked", "http://127.0.0.1/hook", false, true},
		{"localhost blocked", "http://localhost/hook", false, true},
		{"private 10.x blocked", "http://10.0.0.1/hook", false, true},
		{"private 192.168.x blocked", "http://192.168.1.1/hook", false, true},
		{"link-local blocked", "http://169.254.169.254/latest/meta-data/", false, true},
		{"loopback allowed when allowPrivate", "http://127.0.0.1/hook", true, false},
		{"private allowed when allowPrivate", "http://192.168.1.1/hook", true, false},
		{"missing host", "http:///path", false, true},
		{"unspecified 0.0.0.0", "http://0.0.0.0/hook", false, true},
		{"link-local multicast 224.0.0.1", "http://224.0.0.1/hook", false, true},
		// IPv6 transition forms that embed a non-global IPv4. The outer IPv6
		// address is globally scoped, so IsPrivate/IsLoopback miss them.
		{"nat64 loopback blocked", "http://[64:ff9b::127.0.0.1]/hook", false, true},
		{"nat64 link-local blocked", "http://[64:ff9b::169.254.169.254]/hook", false, true},
		{"nat64 private blocked", "http://[64:ff9b::192.168.1.1]/hook", false, true},
		{"nat64 public allowed", "http://[64:ff9b::203.0.113.1]/hook", false, false},
		{"nat64 public allowed when allowPrivate", "http://[64:ff9b::169.254.169.254]/hook", true, false},
		{"ipv4-mapped loopback blocked", "http://[::ffff:127.0.0.1]/hook", false, true},
		{"ipv4-mapped private blocked", "http://[::ffff:192.168.1.1]/hook", false, true},
		{"6to4 private blocked", "http://[2002:c0a8:101::]/hook", false, true},
		{"6to4 public allowed", "http://[2002:cb00:7101::]/hook", false, false}, // 203.0.113.1
		{"ipv4-compatible private blocked", "http://[::192.168.1.1]/hook", false, true},
		{"teredo private client blocked", "http://[2001:0:808:808::3f57:fefe]/hook", false, true},  // client 192.168.1.1
		{"teredo private server blocked", "http://[2001:0:c0a8:101::cb00:7101]/hook", false, true}, // server 192.168.1.1
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := alerts.ValidateWebhookURL(context.Background(), tt.url, tt.allowPrivate)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateWebhookURL(%q, %v) error = %v, wantErr %v", tt.url, tt.allowPrivate, err, tt.wantErr)
			}
		})
	}
}
