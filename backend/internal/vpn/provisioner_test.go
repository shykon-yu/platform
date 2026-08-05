package vpn

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestNewMockProvisioner(t *testing.T) {
	provider, err := New(Config{Mode: "mock"})
	if err != nil || provider == nil {
		t.Fatalf("expected mock provisioner, got provider=%v err=%v", provider, err)
	}
}

func TestNewVPNCmdRequiresConfiguration(t *testing.T) {
	if _, err := New(Config{Mode: "vpncmd"}); err == nil {
		t.Fatal("expected missing vpncmd configuration to fail")
	}
}

func TestVPNCmdRejectsUnsafeIdentifiers(t *testing.T) {
	provider := &vpncmdProvisioner{path: "vpncmd", endpoint: "localhost", password: "secret", logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	err := provider.Provision(context.Background(), Credential{Hub: "hub;bad", Username: "user"})
	if err == nil {
		t.Fatal("expected unsafe hub identifier to fail")
	}
}

func TestVPNCmdRenewOnlyUpdatesExpiration(t *testing.T) {
	var arguments []string
	provider := &vpncmdProvisioner{
		path:     "vpncmd",
		endpoint: "localhost:5555",
		password: "secret",
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		execute: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			arguments = append(arguments, args...)
			return nil, nil
		},
	}
	expiresAt := time.Date(2026, time.August, 3, 12, 30, 0, 0, time.UTC)

	if err := provider.Renew(context.Background(), "we8-room-01", "room-1-user-2", expiresAt); err != nil {
		t.Fatalf("Renew() error = %v", err)
	}
	command := strings.Join(arguments, " ")
	if !strings.Contains(command, "/CMD UserExpiresSet room-1-user-2 /EXPIRES:2026/08/03 12:30:00") {
		t.Fatalf("Renew() arguments = %q", command)
	}
	if strings.Contains(command, "UserDelete") || strings.Contains(command, "UserCreate") {
		t.Fatalf("Renew() must not recreate the connected user: %q", command)
	}
}

func TestVPNCmdRevokeDisconnectsMatchingSessionsBeforeDeletingUser(t *testing.T) {
	commands := make([]string, 0)
	provider := &vpncmdProvisioner{
		path:     "vpncmd",
		endpoint: "localhost:5555",
		password: "secret",
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		execute: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			command := strings.Join(args, " ")
			commands = append(commands, command)
			if strings.Contains(command, "/CMD SessionList") {
				return []byte(`Session Name|SID-TARGET-1
User Name   |room-1-user-2
Session Name|SID-OTHER-1
User Name   |room-1-user-3
Session Name|SID-TARGET-2
User Name   |room-1-user-2
`), nil
			}
			return nil, nil
		},
	}

	if err := provider.Revoke(context.Background(), "we8-room-01", "room-1-user-2"); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	joined := strings.Join(commands, "\n")
	for _, expected := range []string{
		"/CMD SessionList",
		"/CMD SessionDisconnect SID-TARGET-1",
		"/CMD SessionDisconnect SID-TARGET-2",
		"/CMD UserDelete room-1-user-2",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("Revoke() commands missing %q:\n%s", expected, joined)
		}
	}
	if strings.Contains(joined, "SessionDisconnect SID-OTHER-1") {
		t.Fatalf("Revoke() disconnected another user:\n%s", joined)
	}
	if len(commands) != 4 {
		t.Fatalf("Revoke() command count = %d, want 4:\n%s", len(commands), joined)
	}
}
