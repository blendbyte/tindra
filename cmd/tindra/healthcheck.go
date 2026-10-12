package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// healthcheckCmd asks the running server for /healthz. The runtime image has
// no shell or curl, so Docker's HEALTHCHECK runs this subcommand instead.
func healthcheckCmd(cfg config) *cobra.Command {
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "healthcheck",
		Short: "Exit 0 when the server on BIND_ADDR answers /healthz",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			return checkHealth(ctx, cfg.bindAddr)
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", 3*time.Second, "How long to wait for an answer")
	return cmd
}

func checkHealth(ctx context.Context, bindAddr string) error {
	client := &http.Client{}
	url := "http://tindra/healthz"
	if path, ok := strings.CutPrefix(bindAddr, "unix:"); ok {
		// Dial the socket whatever host the URL names.
		client.Transport = &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", path)
			},
		}
	} else {
		host, port, err := net.SplitHostPort(bindAddr)
		if err != nil {
			return fmt.Errorf("parse BIND_ADDR %q: %w", bindAddr, err)
		}
		// A wildcard bind (":8080", "0.0.0.0:8080", "[::]:8080") is reachable on loopback.
		if host == "" || host == "0.0.0.0" || host == "::" {
			host = "127.0.0.1"
		}
		url = "http://" + net.JoinHostPort(host, port) + "/healthz"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("health check: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check: %s returned %s", url, resp.Status)
	}
	return nil
}
