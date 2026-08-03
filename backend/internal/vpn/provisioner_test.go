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
