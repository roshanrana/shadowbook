package chaos

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// CLI drives containers through the docker binary.
//
// and the schedule, never from user input. G204 is about untrusted argv; there
// is no untrusted argv here.
//
// The docker SDK would be a large dependency for four verbs, and the CLI is
// what the runbook tells an operator to use by hand -- so the harness and the
// human do the same thing.
//
//nolint:gosec // Binary and container names come from this repo's compose file
type CLI struct {
	Binary  string
	Timeout time.Duration
}

// NewCLI builds a docker CLI driver.
func NewCLI() *CLI { return &CLI{Binary: "docker", Timeout: 30 * time.Second} }

// Available reports whether a docker daemon can be reached, with the reason if
// not. The ablation runner calls this first so it can fail with something
// actionable instead of a container error forty seconds in.
func (c *CLI) Available(ctx context.Context) (bool, string) {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, c.Binary, "info", "--format", "{{.ServerVersion}}").CombinedOutput() //nolint:gosec // fixed argv
	if err != nil {
		return false, strings.TrimSpace(string(out))
	}
	return true, strings.TrimSpace(string(out))
}

func (c *CLI) run(ctx context.Context, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, c.Binary, args...).CombinedOutput() //nolint:gosec // argv from the compose file and schedule
	if err != nil {
		return fmt.Errorf("chaos: docker %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Kill stops a container abruptly. Not `stop`: a graceful shutdown is not the
// failure Finding 2 is about.
func (c *CLI) Kill(ctx context.Context, container string) error {
	return c.run(ctx, "kill", container)
}

// Start restarts a killed container.
func (c *CLI) Start(ctx context.Context, container string) error {
	return c.run(ctx, "start", container)
}

// BrokerVersion reports the broker image tag, which every artefact records.
func (c *CLI) BrokerVersion(ctx context.Context, container string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, c.Binary, "inspect", "--format", "{{.Config.Image}}", container).Output() //nolint:gosec // fixed argv
	if err != nil {
		return "", fmt.Errorf("chaos: inspect %s: %w", container, err)
	}
	return strings.TrimSpace(string(out)), nil
}

var _ Docker = (*CLI)(nil)
