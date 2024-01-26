package archivehelper

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"io"
	"os"
	"path"
	"path/filepath"

	"github.com/pkg/errors"
)

func ExtractZip(zipPath, outPath string) error {
	zipFile, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zipFile.Close()

	for _, f := range zipFile.File {
		subZipFile, err := f.Open()
		if err != nil {
			return errors.Wrap(err, "failed to open file in zip")
		}

		subOutPath := filepath.Join(outPath, f.Name)

		if f.FileInfo().IsDir() {
			err := os.MkdirAll(subOutPath, 0755)
			if err != nil {
				return errors.Wrap(err, "failed to create zip out directory")
			}
		} else {
			outFile, err := os.Create(subOutPath)
			if err != nil {
				return errors.Wrap(err, "failed to create zip out file")
			}

			_, err = io.Copy(outFile, subZipFile)
			if err != nil {
				return errors.Wrap(err, "failed to copy zip out file")
			}

			_ = outFile.Close()
		}

		_ = subZipFile.Close()
	}

	return nil
}

func ExtractTarGz(targzPath, outPath string) error {
	targzFile, err := os.OpenFile(targzPath, os.O_RDONLY, 0)
	if err != nil {
		return errors.Wrap(err, "failed to open targz file")
	}
	defer targzFile.Close()

	tarFile, err := gzip.NewReader(targzFile)
	if err != nil {
		return errors.Wrap(err, "failed to decompress with gzip")
	}

	tarRdr := tar.NewReader(tarFile)

	for {
		header, err := tarRdr.Next()

		if err == io.EOF {
			break
		}

		if err != nil {
			return errors.Wrap(err, "failed to read next tar file")
		}

		subOutPath := path.Join(outPath, header.Name)

		switch header.Typeflag {
		case tar.TypeDir:
			err := os.Mkdir(subOutPath, 0755)
			if err != nil {
				return errors.Wrap(err, "failed to create tar out directory")
			}
		case tar.TypeReg:
			outFile, err := os.Create(subOutPath)
			if err != nil {
				return errors.Wrap(err, "failed to create tar out file")
			}

			_, err = io.Copy(outFile, tarRdr)
			if err != nil {
				return errors.Wrap(err, "failed to copy tar out file")
			}

			_ = outFile.Close()
		default:
			return errors.New("encountered unexpected entry in tar file")
		}
	}

	return nil
}
