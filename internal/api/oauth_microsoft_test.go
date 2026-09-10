package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/stretchr/testify/require"
)

func TestMicrosoftTenantValidation(t *testing.T) {
	for _, tenant := range []string{"", " \t", "common", "organizations", "consumers", "COMMON", "example.onmicrosoft.com", "not-a-uuid", "00000000-0000-0000-0000-000000000000", "urn:uuid:12345678-1234-1234-1234-123456789abc", "12345678123412341234123456789abc", "../common"} {
		t.Run(tenant, func(t *testing.T) {
			client := &http.Client{Transport: emailVerificationTransport(func(r *http.Request) (*http.Response, error) {
				t.Fatal("invalid tenant must be rejected before discovery")
				return nil, nil
			})}
			p, err := newMicrosoftProvider(oidc.ClientContext(t.Context(), client), tenant, "id", "secret", "https://app.test")
			require.Nil(t, p)
			require.ErrorContains(t, err, "MICROSOFT_TENANT must be a concrete Directory (tenant) ID")
		})
	}
}

func TestLoadMicrosoftProvider(t *testing.T) {
	const tenant = "12345678-1234-1234-1234-123456789abc"
	const issuer = "https://login.microsoftonline.com/" + tenant + "/v2.0"
	for _, scenario := range []string{"valid", "normalized", "missing tenant", "discovery failure", "placeholder issuer", "other tenant issuer"} {
		t.Run(scenario, func(t *testing.T) {
			for _, key := range []string{"OIDC_ISSUER_URL", "GITHUB_CLIENT_ID", "GOOGLE_CLIENT_ID", "ZITADEL_ISSUER_URL", "AUTH0_DOMAIN"} {
				t.Setenv(key, "")
			}
			t.Setenv("OAUTH_REDIRECT_BASE", "https://app.test")
			t.Setenv("MICROSOFT_CLIENT_ID", "client-id")
			t.Setenv("MICROSOFT_CLIENT_SECRET", "secret")
			t.Setenv("MICROSOFT_TENANT", tenant)
			if scenario == "normalized" {
				t.Setenv("MICROSOFT_TENANT", " "+strings.ToUpper(tenant)+" ")
			}
			if scenario == "missing tenant" {
				t.Setenv("MICROSOFT_TENANT", "")
			}
			var logs bytes.Buffer
			logger := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(logger) })
			calls := 0
			client := &http.Client{Transport: emailVerificationTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, issuer+"/.well-known/openid-configuration", r.URL.String())
				if scenario == "discovery failure" {
					return nil, errors.New("discovery unavailable")
				}
				discoveredIssuer := issuer
				if scenario == "placeholder issuer" {
					discoveredIssuer = "https://login.microsoftonline.com/{tenantid}/v2.0"
				}
				if scenario == "other tenant issuer" {
					discoveredIssuer = "https://login.microsoftonline.com/87654321-1234-1234-1234-123456789abc/v2.0"
				}
				response := httptest.NewRecorder()
				response.Header().Set("Content-Type", "application/json")
				require.NoError(t, json.NewEncoder(response).Encode(map[string]any{
					"issuer": discoveredIssuer, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token", "jwks_uri": issuer + "/keys", "id_token_signing_alg_values_supported": []string{"RS256"},
				}))
				return response.Result(), nil
			})}
			providers := LoadOAuthProviders(oidc.ClientContext(t.Context(), client))
			require.True(t, oauthConfigured(), "SSO policy must remain active even if provider initialization fails")
			if scenario == "valid" || scenario == "normalized" {
				require.Len(t, providers, 1)
				require.Equal(t, "microsoft", providers[0].Name())
				require.Contains(t, providers[0].AuthCodeURL("state", "verifier"), issuer+"/authorize?")
			} else {
				require.Empty(t, providers)
				require.Contains(t, logs.String(), "oauth provider disabled")
				if scenario == "missing tenant" {
					require.Contains(t, logs.String(), "MICROSOFT_TENANT must be a concrete Directory (tenant) ID")
				}
			}
			if scenario == "missing tenant" {
				require.Zero(t, calls)
			} else {
				require.Equal(t, 1, calls)
			}
		})
	}
}
