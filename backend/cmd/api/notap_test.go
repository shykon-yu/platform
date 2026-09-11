package main

import (
	"testing"
	"time"
)

func TestNoTapLeasePayloadUsesDedicatedRelay(t *testing.T) {
	a := &app{config: config{
		openVPNClientHost: "tap.example.test",
		n2nClientHost: "n2n.example.test",
		n2nClientPort: 22222,
		n2nRoomPorts: map[int64]int{},
		noTapRelayHost:    "notap.example.test",
		noTapRelayPort:    22333,
		noTapRelayToken:   "notap-relay-secret",
	}}
	expiresAt := time.Now().Add(30 * time.Minute).Truncate(time.Second)

	got := a.noTapLeasePayload(2, "notap-02", "10.122.2.0/24", "10.122.2.10", "relay-user", "direct", expiresAt)

	if got.RoomID != 2 || got.VirtualIP != "10.122.2.10" || got.LogicalIP != got.VirtualIP {
		t.Fatalf("No-TAP address payload = %#v", got)
	}
	if got.SubnetCIDR != "10.122.2.0/24" || got.Community != "notap-02" {
		t.Fatalf("No-TAP room payload = %#v", got)
	}
	if got.ConnectionMode != "direct" {
		t.Fatalf("No-TAP connection mode = %q, want direct", got.ConnectionMode)
	}
	if got.RelayHost != "notap.example.test" || got.RelayPort != 22333 || got.RelayToken != "notap-relay-secret" {
		t.Fatalf("No-TAP relay payload = %#v", got)
	}
	if got.RelayHost == a.config.openVPNClientHost {
		t.Fatal("No-TAP lease used the TAP/n2n host")
	}
	if got.ServerHost != "" || got.ServerPort != 0 {
		t.Fatalf("direct No-TAP payload leaked TAP server fields = %#v", got)
	}
	if !got.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("No-TAP expiry = %v, want %v", got.ExpiresAt, expiresAt)
	}
}

func TestNoTapLeasePayloadUsesTapFieldsOnlyForTap(t *testing.T) {
	a := &app{config: config{n2nClientHost: "n2n.example.test", n2nClientPort: 22222, n2nRoomPorts: map[int64]int{}}}
	got := a.noTapLeasePayload(5, "notap-05", "10.222.5.0/24", "10.222.5.10", "tap-user", "tap", time.Now())
	if got.ServerHost != "n2n.example.test" || got.ServerPort != 22222 || got.RelayHost != "" || got.IceStunHost != "" {
		t.Fatalf("TAP payload transport fields = %#v", got)
	}
}

func TestNoTapLeasePayloadUsesICEFallbackForWireGuard(t *testing.T) {
	a := &app{config: config{
		noTapRelayHost:    "relay.example.test",
		noTapRelayPort:    22333,
		noTapRelayToken:   "relay-secret",
		noTapIceStunHost:  "stun.example.test",
		noTapIceStunPort:  3478,
	}}
	got := a.noTapLeasePayload(7, "notap-07", "10.222.7.0/24", "10.222.7.10", "wg-user", "wireguard", time.Now())
	if got.ConnectionMode != "wireguard" || got.IceStunHost != "stun.example.test" || got.IceStunPort != 3478 {
		t.Fatalf("WireGuard payload did not preserve ICE fallback = %#v", got)
	}
	if got.ServerHost != "" || got.ServerPort != 0 {
		t.Fatalf("WireGuard payload leaked TAP fields = %#v", got)
	}
}

func TestParseIPv4RejectsInvalidNoTapAddress(t *testing.T) {
	for _, value := range []string{"", "10.122.1", "10.122.1.256", "10.122.-1.10"} {
		if got := parseIPv4(value); got != nil {
			t.Fatalf("parseIPv4(%q) = %v, want nil", value, got)
		}
	}
}
