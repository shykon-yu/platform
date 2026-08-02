package vpn

import (
	"context"
	"io"
	"log/slog"
	"testing"
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
