package webhelper

import (
	"fmt"
	"github.com/pkg/errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

func DownloadFileFromURL(url string, destPath string) error {
	// Ensure the directory exists
	if err := os.MkdirAll(destPath, 0755); err != nil {
		return err
	}

	filePath := filepath.Join(destPath, filepath.Base(url))
	logFile, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer logFile.Close()
	// Perform the HTTP GET request to download the file
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Check if the response status code indicates success
	if resp.StatusCode != http.StatusOK {
		return errors.New(fmt.Sprintf("server returned non-200 status code: %d "+
			"while downloading from url", resp.StatusCode))
	}

	// Copy the response body to the file
	_, err = io.Copy(logFile, resp.Body)
	if err != nil {
		return err
	}

	return nil
}
