package caocontrol

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"runtime"

	"github.com/couchbaselabs/cbdinocluster/utils/archivehelper"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

func DownloadLocalCaoTools(
	ctx context.Context,
	logger *zap.Logger,
	installPath string,
	version string,
	isOpenShift bool,
) error {
	osName := runtime.GOOS
	archName := runtime.GOARCH
	return DownloadCaoTools(ctx, logger, installPath, version, osName, archName, isOpenShift)
}

func DownloadCaoTools(
	ctx context.Context,
	logger *zap.Logger,
	installPath string,
	version string,
	osName string,
	archName string,
	isOpenShift bool,
) error {
	if osName == "darwin" {
		osName = "macos"
	}

	var archiveType string
	if osName == "linux" {
		archiveType = "tar.gz"
	} else if osName == "macos" {
		archiveType = "zip"
	} else if osName == "windows" {
		archiveType = "zip"
	} else {
		return errors.New("unsupported osName")
	}

	cbPlatform := "kubernetes"
	if isOpenShift {
		cbPlatform = "openshift"
	}

	/*
		https://packages.couchbase.com/releases/couchbase-operator/2.6.0/couchbase-autonomous-operator_2.6.0-kubernetes-macos-arm64.zip
		https://packages.couchbase.com/releases/couchbase-operator/2.6.0/couchbase-autonomous-operator_2.6.0-openshift-macos-amd64.zip
		https://packages.couchbase.com/releases/couchbase-operator/2.6.0/couchbase-autonomous-operator_2.6.0-openshift-linux-amd64.tar.gz
	*/
	finalUri := fmt.Sprintf("https://packages.couchbase.com/releases/couchbase-operator/%s/couchbase-autonomous-operator_%s-%s-%s-%s.%s",
		version, version, cbPlatform, osName, archName, archiveType)

	logger.Debug("generated download path", zap.String("path", finalUri))

	tmpFile, err := os.CreateTemp("", "caotools")
	if err != nil {
		return errors.Wrap(err, "failed to create temporary file")
	}

	resp, err := http.Get(finalUri)
	if err != nil {
		return errors.Wrap(err, "failed to download file")
	}
	defer resp.Body.Close()

	_, err = io.Copy(tmpFile, resp.Body)
	if err != nil {
		return errors.Wrap(err, "error occured while downloading files")
	}

	tmpDir, err := os.MkdirTemp("", "caotools")
	if err != nil {
		return errors.Wrap(err, "failed to create temporary unarchive directory")
	}

	logger.Debug("file downloaded, extracting...", zap.String("path", tmpDir))

	if archiveType == "tar.gz" {
		err := archivehelper.ExtractTarGz(tmpFile.Name(), tmpDir)
		if err != nil {
			return errors.Wrap(err, "failed to extract tar.gz archive")
		}
	} else if archiveType == "zip" {
		err := archivehelper.ExtractZip(tmpFile.Name(), tmpDir)
		if err != nil {
			return errors.Wrap(err, "failed to extract zip archive")
		}
	} else {
		return errors.New("unsupported archive type")
	}

	logger.Debug("extracted, moving to final location...", zap.String("path", installPath))

	// best-effort try to remove the temporary file
	_ = os.Remove(tmpFile.Name())

	archDirs, err := os.ReadDir(tmpDir)
	if err != nil {
		return errors.Wrap(err, "failed to read unarchived directories")
	}

	if len(archDirs) != 1 {
		return errors.New("failed to find operator output files")
	}

	archDirName := archDirs[0].Name()
	archDirPath := path.Join(tmpDir, archDirName)

	// Attempt to chmod the binaries
	binDirPath := path.Join(archDirPath, "bin")
	binDirs, err := os.ReadDir(binDirPath)
	if err != nil {
		return errors.Wrap(err, "failed to list bin directory")
	}

	for _, binFile := range binDirs {
		if binFile.Type().IsRegular() {
			binFilePath := path.Join(binDirPath, binFile.Name())
			err := os.Chmod(binFilePath, 0755)
			if err != nil {
				return errors.Wrap(err, "failed to chmod bin file")
			}
		}
	}

	err = os.MkdirAll(path.Dir(installPath), 0755)
	if err != nil {
		return errors.Wrap(err, "failed to create parent directories")
	}

	err = os.Rename(archDirPath, installPath)
	if err != nil {
		return errors.Wrap(err, "failed to move files to final location")
	}

	// best-effort try to remove the temporary directory
	_ = os.RemoveAll(tmpDir)

	return nil
}
