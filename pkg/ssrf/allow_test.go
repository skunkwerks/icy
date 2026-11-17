package ssrf

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseAllowedNetworks(t *testing.T) {
	good := []string{
		"10.0.0.0/8",
		" 192.168.1.20 ",
		"fd00::/8",
		"::1",
		"::ffff:172.16.0.0/108",
		"::ffff:10.1.2.3",
	}
	nets, err := ParseAllowedNetworks(good)
	if err != nil {
		t.Fatalf("ParseAllowedNetworks(%q) error = %v", good, err)
	}
	if len(nets) != len(good) {
		t.Fatalf("got %d networks, want %d", len(nets), len(good))
	}
	// Mapped forms are stored unmapped.
	if got := nets[4].String(); got != "172.16.0.0/12" {
		t.Errorf("mapped prefix = %s, want 172.16.0.0/12", got)
	}
	if got := nets[5].String(); got != "10.1.2.3/32" {
		t.Errorf("mapped address = %s, want 10.1.2.3/32", got)
	}

	for _, bad := range []string{
		"",
		"ci.internal.example",
		"localhost",
		"10.0.0.0/33",
		"10.0.0.1:8080",
		"http://10.0.0.1/",
		"::ffff:10.0.0.0/64",
	} {
		if _, err := ParseAllowedNetworks([]string{bad}); err == nil {
			t.Errorf("ParseAllowedNetworks(%q) succeeded, want error", bad)
		}
	}
}

func TestAllowedNetworksContains(t *testing.T) {
	allow, err := ParseAllowedNetworks([]string{"10.1.0.0/16", "127.0.0.1", "fd12::/16"})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		ip      string
		allowed bool
	}{
		{"10.1.2.3", true},
		{"::ffff:10.1.2.3", true},
		{"10.2.0.1", false},
		{"127.0.0.1", true},
		{"::ffff:127.0.0.1", true},
		{"127.0.0.2", false},
		{"fd12::1", true},
		{"fd13::1", false},
		{"169.254.169.254", false},
	}
	for _, tt := range tests {
		if got := allow.Contains(net.ParseIP(tt.ip)); got != tt.allowed {
			t.Errorf("Contains(%s) = %v, want %v", tt.ip, got, tt.allowed)
		}
	}

	if allow.Contains(net.IP{1, 2, 3}) {
		t.Error("Contains(unparseable) = true, want false")
	}
	if !allow.blocked(net.IP{1, 2, 3}) {
		t.Error("blocked(unparseable) = false, want true")
	}
	var none AllowedNetworks
	if none.Contains(net.ParseIP("10.1.2.3")) {
		t.Error("empty allowlist contains an address")
	}
}

func TestValidateURLWithAllowlist(t *testing.T) {
	allow, err := ParseAllowedNetworks([]string{"10.1.0.0/16", "192.168.5.7", "fd12::/16"})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		url     string
		allowed bool
	}{
		{"http://10.1.2.3:8080/hook", true},
		{"http://[::ffff:10.1.2.3]/hook", true},
		{"https://192.168.5.7/hook", true},
		{"http://[fd12::1]/hook", true},
		{"http://192.168.5.8/hook", false},
		{"http://10.2.0.1/hook", false},
		{"http://127.0.0.1/hook", false},
		{"http://[::ffff:127.0.0.1]/hook", false},
		{"http://169.254.169.254/latest/meta-data/", false},
		{"http://localhost/hook", false},
		{"ftp://10.1.2.3/hook", false},
	}
	for _, tt := range tests {
		err := ValidateURLWithAllowlist(tt.url, allow)
		if tt.allowed && err != nil {
			t.Errorf("ValidateURLWithAllowlist(%q) = %v, want nil", tt.url, err)
		}
		if !tt.allowed && err == nil {
			t.Errorf("ValidateURLWithAllowlist(%q) = nil, want error", tt.url)
		}
	}

	// Without an allowlist the same private address is refused.
	if err := ValidateURL("http://10.1.2.3:8080/hook"); !errors.Is(err, ErrPrivateIP) {
		t.Errorf("ValidateURL without allowlist = %v, want ErrPrivateIP", err)
	}
}

func TestSecureClientWithAllowlistDials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	get := func(client *http.Client, url string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if resp != nil {
			resp.Body.Close() //nolint:errcheck
		}
		return err
	}

	// The httptest server listens on 127.0.0.1.
	loopback, err := ParseAllowedNetworks([]string{"127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := get(NewSecureClientWithAllowlist(loopback), srv.URL); err != nil {
		t.Errorf("allowed loopback dial failed: %v", err)
	}

	// The same address written as IPv4-mapped IPv6 is allowed too.
	mapped, err := ParseAllowedNetworks([]string{"::ffff:127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := get(NewSecureClientWithAllowlist(mapped), srv.URL); err != nil {
		t.Errorf("allowed mapped loopback dial failed: %v", err)
	}

	// A different allowed range does not open loopback.
	other, err := ParseAllowedNetworks([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	if err := get(NewSecureClientWithAllowlist(other), srv.URL); !errors.Is(err, ErrPrivateIP) {
		t.Errorf("dial outside allowlist = %v, want ErrPrivateIP", err)
	}
	if err := get(NewSecureClient(), srv.URL); !errors.Is(err, ErrPrivateIP) {
		t.Errorf("dial without allowlist = %v, want ErrPrivateIP", err)
	}

	// Other blocked ranges stay blocked at dial time.
	transport := NewSecureClientWithAllowlist(loopback).Transport.(*http.Transport)
	for _, addr := range []string{"127.0.0.2:80", "169.254.169.254:80", "10.0.0.1:80", "[::ffff:169.254.169.254]:80"} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		conn, err := transport.DialContext(ctx, "tcp", addr)
		cancel()
		if conn != nil {
			conn.Close() //nolint:errcheck
		}
		if !errors.Is(err, ErrPrivateIP) {
			t.Errorf("dial %s = %v, want ErrPrivateIP", addr, err)
		}
	}
}
