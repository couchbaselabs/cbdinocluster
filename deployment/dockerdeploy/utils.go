package dockerdeploy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

func dockerBuildAndPipe(ctx context.Context, logger *zap.Logger, cli *client.Client, buildContext io.Reader, options types.ImageBuildOptions) error {
	buildResp, err := cli.ImageBuild(ctx, buildContext, options)
	if err != nil {
		return errors.Wrap(err, "failed to build image")
	}
	defer buildResp.Body.Close()

	pipeRdr, pipeWrt := io.Pipe()
	defer pipeWrt.Close()
	go func() {
		scanner := bufio.NewScanner(pipeRdr)
		for scanner.Scan() {
			logger.Debug("docker build output", zap.String("text", scanner.Text()))
		}
	}()

	dec := json.NewDecoder(buildResp.Body)
	for dec.More() {
		var streamMsg struct {
			Stream string `json:"stream"`
			Error  string `json:"error"`
		}
		err := dec.Decode(&streamMsg)
		if err != nil {
			return errors.Wrap(err, "json decode failure while reading build progress")
		}

		if streamMsg.Error != "" {
			return errors.Wrap(errors.New(streamMsg.Error), "failed during image building")
		}

		pipeWrt.Write([]byte(streamMsg.Stream))
	}

	return nil
}

func dockerPullAndPipe(ctx context.Context, logger *zap.Logger, cli *client.Client, refStr string, options types.ImagePullOptions) error {
	pullResp, err := cli.ImagePull(ctx, refStr, options)
	if err != nil {
		return errors.Wrap(err, "failed to pull image")
	}
	defer pullResp.Close()

	dec := json.NewDecoder(pullResp)

	for dec.More() {
		var streamMsg struct {
			Status string `json:"status"`
		}
		err := dec.Decode(&streamMsg)
		if err != nil {
			return errors.Wrap(err, "json decode failure while reading pull progress")
		}

		switch streamMsg.Status {
		case "Waiting":
		case "Downloading":
		case "Extracting":
		default:
			logger.Debug("docker pull output", zap.String("text", streamMsg.Status))
		}
	}

	return nil
}

func dockerExecAndPipe(ctx context.Context, logger *zap.Logger, cli *client.Client, containerID string, cmd []string) error {
	execID, err := cli.ContainerExecCreate(ctx, containerID, types.ExecConfig{
		AttachStdout: true,
		AttachStderr: true,
		Tty:          true,
		Cmd:          cmd,
	})
	if err != nil {
		return errors.Wrap(err, "failed to create exec")
	}

	resp, err := cli.ContainerExecAttach(ctx, execID.ID, types.ExecStartCheck{
		Tty: true,
	})
	if err != nil {
		return errors.Wrap(err, "failed to start exec")
	}

	scanner := bufio.NewScanner(resp.Reader)
	for scanner.Scan() {
		line := scanner.Text()

		logger.Debug("docker exec output", zap.String("text", line))
	}

	res, err := cli.ContainerExecInspect(ctx, execID.ID)
	if err != nil {
		return errors.Wrap(err, "failed to inspect exec")
	}

	if res.ExitCode != 0 {
		return fmt.Errorf("failed to execute process (exit code: %d)", res.ExitCode)
	}

	return nil
}
