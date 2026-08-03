package vpn

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"time"
)

type Credential struct {
	Hub       string
	Username  string
	Password  string
	ExpiresAt time.Time
}

type Provisioner interface {
	Provision(context.Context, Credential) error
	Renew(context.Context, string, string, time.Time) error
	Revoke(context.Context, string, string) error
}

type Config struct {
	Mode          string
	VPNCmdPath    string
	AdminEndpoint string
	AdminPassword string
	Logger        *slog.Logger
}

func New(config Config) (Provisioner, error) {
	if config.Mode == "" || config.Mode == "mock" {
		return mockProvisioner{}, nil
	}
	if config.Mode != "vpncmd" {
		return nil, fmt.Errorf("unsupported SoftEther mode %q", config.Mode)
	}
	if config.VPNCmdPath == "" || config.AdminEndpoint == "" || config.AdminPassword == "" {
		return nil, errors.New("vpncmd mode requires path, admin endpoint and admin password")
	}
	return &vpncmdProvisioner{path: config.VPNCmdPath, endpoint: config.AdminEndpoint, password: config.AdminPassword, logger: config.Logger}, nil
}

type mockProvisioner struct{}

func (mockProvisioner) Provision(context.Context, Credential) error { return nil }
func (mockProvisioner) Renew(context.Context, string, string, time.Time) error {
	return nil
}
func (mockProvisioner) Revoke(context.Context, string, string) error { return nil }

type vpncmdProvisioner struct {
	path, endpoint, password string
	logger                   *slog.Logger
	execute                  func(context.Context, string, ...string) ([]byte, error)
}

var safeIdentifier = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,96}$`)

func (p *vpncmdProvisioner) Provision(ctx context.Context, credential Credential) error {
	if !safeIdentifier.MatchString(credential.Hub) || !safeIdentifier.MatchString(credential.Username) {
		return errors.New("invalid SoftEther hub or username")
	}
	_ = p.command(ctx, credential.Hub, "UserDelete", credential.Username)
	note := "expires=" + credential.ExpiresAt.UTC().Format(time.RFC3339)
	if err := p.command(ctx, credential.Hub, "UserCreate", credential.Username, "/GROUP:none", "/REALNAME:none", "/NOTE:"+note); err != nil {
		return err
	}
	if err := p.command(ctx, credential.Hub, "UserPasswordSet", credential.Username, "/PASSWORD:"+credential.Password); err != nil {
		_ = p.command(ctx, credential.Hub, "UserDelete", credential.Username)
		return err
	}
	expires := softEtherExpiresAt(credential.ExpiresAt)
	if err := p.command(ctx, credential.Hub, "UserExpiresSet", credential.Username, "/EXPIRES:"+expires); err != nil {
		_ = p.command(ctx, credential.Hub, "UserDelete", credential.Username)
		return err
	}
	return nil
}

func (p *vpncmdProvisioner) Revoke(ctx context.Context, hub, username string) error {
	if !safeIdentifier.MatchString(hub) || !safeIdentifier.MatchString(username) {
		return errors.New("invalid SoftEther hub or username")
	}
	return p.command(ctx, hub, "UserDelete", username)
}

func (p *vpncmdProvisioner) Renew(ctx context.Context, hub, username string, expiresAt time.Time) error {
	if !safeIdentifier.MatchString(hub) || !safeIdentifier.MatchString(username) {
		return errors.New("invalid SoftEther hub or username")
	}
	expires := softEtherExpiresAt(expiresAt)
	return p.command(ctx, hub, "UserExpiresSet", username, "/EXPIRES:"+expires)
}

func softEtherExpiresAt(expiresAt time.Time) string {
	return expiresAt.UTC().Format("2006/01/02 15:04:05")
}

func (p *vpncmdProvisioner) command(ctx context.Context, hub, command string, args ...string) error {
	commandCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	baseArgs := []string{p.endpoint, "/SERVER", "/PASSWORD:" + p.password, "/ADMINHUB:" + hub, "/CMD", command}
	execute := p.execute
	if execute == nil {
		execute = func(ctx context.Context, path string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, path, args...).CombinedOutput()
		}
	}
	_, err := execute(commandCtx, p.path, append(baseArgs, args...)...)
	if err != nil {
		p.logger.Error("vpncmd failed", "hub", hub, "command", command, "error", err)
		return fmt.Errorf("vpncmd %s failed: %w", command, err)
	}
	return nil
}
