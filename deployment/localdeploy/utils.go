package localdeploy

import (
	"bufio"
	"io"
	"os/exec"

	"go.uber.org/zap"
)

func execAndPipe(logger *zap.Logger, name string, args ...string) error {
	logger.Debug("executing command",
		zap.String("exec", name),
		zap.Strings("args", args))

	outPipeRdr, outPipeWrt := io.Pipe()
	defer outPipeWrt.Close()
	go func() {
		scanner := bufio.NewScanner(outPipeRdr)
		for scanner.Scan() {
			logger.Debug("exec output", zap.String("text", scanner.Text()))
		}
	}()

	errPipeRdr, errPipeWrt := io.Pipe()
	defer errPipeWrt.Close()
	go func() {
		scanner := bufio.NewScanner(errPipeRdr)
		for scanner.Scan() {
			logger.Debug("exec error output", zap.String("text", scanner.Text()))
		}
	}()

	cmd := exec.Command(name, args...)
	cmd.Stdout = outPipeWrt
	cmd.Stderr = errPipeWrt
	return cmd.Run()
}
