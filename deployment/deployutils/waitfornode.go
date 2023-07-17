package deployutils

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/pkg/errors"
)

func WaitForNode(ctx context.Context, hostname string) error {
	log.Printf("waiting for couchbase to start")

	for {
		httpCli := &http.Client{}

		testUrl := fmt.Sprintf("http://%s:8091", hostname)
		testReq, err := http.NewRequest("GET", testUrl, nil)
		if err != nil {
			return errors.Wrap(err, "failed to create test request")
		}

		resp, err := httpCli.Do(testReq)
		if err != nil {

			select {
			case <-time.After(1 * time.Second):
				// continue
			case <-ctx.Done():
				return errors.Wrap(ctx.Err(), "context finished while waiting for node to start")
			}
			time.Sleep(1 * time.Second)
			continue
		}

		resp.Body.Close()

		break
	}
	log.Printf("ready!")

	return nil
}
