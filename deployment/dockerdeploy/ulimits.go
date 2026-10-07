package dockerdeploy

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	units "github.com/docker/go-units"
	"github.com/moby/moby/client"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

// EnvDockerNofile is the environment variable which can be used to control the
// nofile ulimit applied to the containers that cbdinocluster deploys.  It
// accepts one of:
//   - unset, empty or "auto": request DefaultDockerNofile, and if the docker
//     daemon is not permitted to grant that, fall back to inheriting the
//     daemon's own limit (the maximum it is able to grant).
//   - a positive integer: request exactly that limit, failing if the docker
//     daemon cannot grant it.
//   - "0" or "none": do not request any nofile limit, the containers inherit
//     the docker daemon's limit.
const EnvDockerNofile = "CBDINOCLUSTER_DOCKER_NOFILE"

// DefaultDockerNofile is the nofile limit requested when none is configured.
const DefaultDockerNofile = 200000

type nofileMode int

const (
	nofileModeAuto nofileMode = iota
	nofileModeFixed
	nofileModeInherit
)

func parseDockerNofile(value string) (nofileMode, int64, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", "auto":
		return nofileModeAuto, DefaultDockerNofile, nil
	case "0", "none":
		return nofileModeInherit, 0, nil
	}

	limit, err := strconv.ParseInt(value, 10, 64)
	if err != nil || limit < 0 {
		return 0, 0, errors.Errorf(
			"invalid %s value %q (expected a positive integer, \"auto\" or \"none\")",
			EnvDockerNofile, value)
	}

	return nofileModeFixed, limit, nil
}

// isRlimitError checks whether a container start failed because the runtime
// was not permitted to apply the requested resource limits.
func isRlimitError(err error) bool {
	if err == nil {
		return false
	}

	lowerText := strings.ToLower(err.Error())
	return strings.Contains(lowerText, "rlimit")
}

// nofileUlimits returns the ulimits to apply to a new container, and whether
// a failure to apply them may be recovered from by retrying without them.
func (c *Controller) nofileUlimits() ([]*units.Ulimit, bool, error) {
	mode, limit, err := parseDockerNofile(os.Getenv(EnvDockerNofile))
	if err != nil {
		return nil, false, err
	}

	if mode == nofileModeInherit || (mode == nofileModeAuto && c.nofileUnsupported.Load()) {
		return nil, false, nil
	}

	return []*units.Ulimit{
		{Name: "nofile", Soft: limit, Hard: limit},
	}, mode == nofileModeAuto, nil
}

// createAndStartContainer creates and starts a container, applying the
// configured nofile ulimit.  If the limit was not explicitly configured and the
// docker daemon refuses to apply it (for instance in sandboxed environments
// where the daemon itself runs with a lower limit), the container is recreated
// without an explicit limit so that it inherits the daemon's maximum instead.
// The preStart callback, if provided, is invoked between creation and start of
// the container.
func (c *Controller) createAndStartContainer(
	ctx context.Context,
	logger *zap.Logger,
	opts client.ContainerCreateOptions,
	preStart func(containerID string) error,
) (string, error) {
	ulimits, canFallback, err := c.nofileUlimits()
	if err != nil {
		return "", err
	}

	for {
		createOpts := opts
		hostConfig := *opts.HostConfig
		hostConfig.Ulimits = ulimits
		createOpts.HostConfig = &hostConfig

		createResult, err := c.DockerCli.ContainerCreate(ctx, createOpts)
		if err != nil {
			return "", errors.Wrap(err, "failed to create container")
		}

		containerID := createResult.ID

		if preStart != nil {
			err = preStart(containerID)
			if err != nil {
				c.cleanupFailedContainer(ctx, logger, containerID)
				return "", err
			}
		}

		logger.Debug("container created, starting", zap.String("container", containerID))

		_, err = c.DockerCli.ContainerStart(ctx, containerID, client.ContainerStartOptions{})
		if err == nil {
			return containerID, nil
		}

		if !isRlimitError(err) {
			c.cleanupFailedContainer(ctx, logger, containerID)
			return "", errors.Wrap(err, "failed to start container")
		}
		if !canFallback {
			c.cleanupFailedContainer(ctx, logger, containerID)
			return "", errors.Wrapf(err,
				"failed to start container (docker could not apply the nofile limit, adjust %s)",
				EnvDockerNofile)
		}

		logger.Warn("docker refused the requested nofile limit, falling back to the docker daemon's limit",
			zap.Error(err),
			zap.String("hint", "set "+EnvDockerNofile+" to choose a specific limit"))

		// The replacement container reuses the same name, so the old one must
		// be completely gone before we recreate it.
		removeErr := c.removeContainerAndWait(ctx, containerID)
		if removeErr != nil {
			return "", errors.Wrap(removeErr, "failed to remove container after nofile limit failure")
		}

		c.nofileUnsupported.Store(true)
		ulimits = nil
		canFallback = false
	}
}

// removeContainerAndWait force-removes a container and waits for it to no
// longer exist.  Containers created with AutoRemove are removed by the daemon
// itself when they fail to start, so a container which is already gone (or
// whose removal is already in progress) is not treated as an error.
func (c *Controller) removeContainerAndWait(ctx context.Context, containerID string) error {
	_, err := c.DockerCli.ContainerRemove(ctx, containerID, client.ContainerRemoveOptions{
		Force: true,
	})
	if err != nil && !cerrdefs.IsNotFound(err) &&
		!strings.Contains(strings.ToLower(err.Error()), "already in progress") {
		return err
	}

	for {
		_, err := c.DockerCli.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
		if cerrdefs.IsNotFound(err) {
			return nil
		} else if err != nil {
			return errors.Wrap(err, "failed to check container removal")
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// cleanupFailedContainer best-effort removes a container that was created but
// could not be started, so that failed deployments do not leave it behind.
func (c *Controller) cleanupFailedContainer(ctx context.Context, logger *zap.Logger, containerID string) {
	err := c.removeContainerAndWait(ctx, containerID)
	if err != nil {
		logger.Warn("failed to remove container after failed start",
			zap.String("container", containerID),
			zap.Error(err))
	}
}
