package filehelper

import (
	"io"
	"os"
	"path/filepath"

	"github.com/pkg/errors"
)

func MoveDir(src, dest string) error {
	if err := CopyDir(src, dest); err != nil {
		return errors.Wrapf(err, "failed to recursively copy '%s' to '%s'", src, dest)
	}

	if err := os.RemoveAll(src); err != nil {
		return errors.Wrapf(err, "failed to remove the source directory '%s'", src)
	}

	return nil
}

func CopyDir(src, dest string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return errors.Wrapf(err, "failed to get mode of the source directory '%s' for copy", src)
	}

	if err = os.MkdirAll(dest, info.Mode()); err != nil {
		return errors.Wrapf(err, "failed to create the destination directory '%s' for copy", dest)
	}

	dir, err := os.Open(src)
	if err != nil {
		return errors.Wrapf(err, "failed to open the source directory '%s' for copy", src)
	}
	defer dir.Close()

	items, err := dir.Readdir(-1)
	if err != nil && err != io.EOF {
		return errors.Wrapf(err, "failed to read the source directory '%s' for copy", src)
	}

	for _, item := range items {
		srcPath := filepath.Join(src, item.Name())
		destPath := filepath.Join(dest, item.Name())

		if item.IsDir() {
			if err := CopyDir(srcPath, destPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, destPath); err != nil {
				return err
			}
		}
	}

	return nil
}

func copyFile(src, dest string) error {
	source, err := os.Open(src)
	if err != nil {
		return errors.Wrapf(err, "failed to open source file '%s' for copy", src)
	}
	defer source.Close()

	destination, err := os.Create(dest)
	if err != nil {
		return errors.Wrapf(err, "failed to create destination file '%s' for copy", dest)
	}
	defer destination.Close()

	info, err := os.Lstat(src)
	if err != nil {
		return errors.Wrapf(err, "failed to get mode of the source file '%s'", src)
	}

	if err := destination.Chmod(info.Mode()); err != nil {
		return errors.Wrapf(err, "failed to set mode of the destination file '%s'", dest)
	}

	_, err = io.Copy(destination, source)
	return errors.Wrapf(err, "failed to copy file '%s' to '%s'", src, dest)

}
