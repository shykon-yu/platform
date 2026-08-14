package main

import (
	"testing"
	"time"
)

func TestNoTapLeasePayloadUsesDedicatedRelay(t *testing.T) {
	a := &app{config: config{
		openVPNClientHost: "tap.example.test",
		noTapRelayHost:    "notap.example.test",
		noTapRelayPort:    22333,
		noTapRelayToken:   "notap-relay-secret",
	}}
	expiresAt := time.Now().Add(30 * time.Minute).Truncate(time.Second)

	got := a.noTapLeasePayload(2, "notap-02", "10.122.2.0/24", "10.122.2.10", "relay-user", expiresAt)

	if got.RoomID != 2 || got.VirtualIP != "10.122.2.10" || got.LogicalIP != got.VirtualIP {
		t.Fatalf("No-TAP address payload = %#v", got)
	}
	if got.SubnetCIDR != "10.122.2.0/24" || got.Community != "notap-02" {
		t.Fatalf("No-TAP room payload = %#v", got)
	}
	if got.RelayHost != "notap.example.test" || got.RelayPort != 22333 || got.RelayToken != "notap-relay-secret" {
		t.Fatalf("No-TAP relay payload = %#v", got)
	}
	if got.RelayHost == a.config.openVPNClientHost {
		t.Fatal("No-TAP lease used the TAP/n2n host")
	}
	if !got.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("No-TAP expiry = %v, want %v", got.ExpiresAt, expiresAt)
	}
}

func TestParseIPv4RejectsInvalidNoTapAddress(t *testing.T) {
	for _, value := range []string{"", "10.122.1", "10.122.1.256", "10.122.-1.10"} {
		if got := parseIPv4(value); got != nil {
			t.Fatalf("parseIPv4(%q) = %v, want nil", value, got)
		}
	}
}
