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
