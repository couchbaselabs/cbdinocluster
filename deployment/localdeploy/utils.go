package localdeploy

import (
	"log"
	"os"
	"os/exec"
	"strings"
)

func execAndPipe(name string, args ...string) error {
	cmdStr := name + " " + strings.Join(args, " ")
	log.Printf("Executing command: %s", cmdStr)

	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
