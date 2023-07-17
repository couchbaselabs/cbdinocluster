package tarhelper

import (
	"archive/tar"
	"embed"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/pkg/errors"
)

type TarBuilder struct {
	tw *tar.Writer
}

func NewTarBuilder(w io.Writer) (*TarBuilder, error) {
	return &TarBuilder{
		tw: tar.NewWriter(w),
	}, nil
}

func (b *TarBuilder) AddFile(f fs.File, targetPath string) error {
	fileStat, err := f.Stat()
	if err != nil {
		return errors.Wrap(err, "failed to stat file")
	}

	hdr, err := tar.FileInfoHeader(fileStat, "")
	if err != nil {
		return errors.Wrap(err, "failed to generate tar file header")
	}

	hdr.Name = targetPath

	b.tw.WriteHeader(hdr)

	_, err = io.Copy(b.tw, f)
	if err != nil {
		return errors.Wrap(err, "failed to write file to tar")
	}

	return nil
}

func (b *TarBuilder) AddLocalFile(localPath string, targetPath string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return errors.Wrapf(err, "failed to open local file '%s'", localPath)
	}

	return b.AddFile(f, targetPath)
}

func (b *TarBuilder) AddEmbedFile(efs *embed.FS, embedPath, targetPath string) error {
	f, err := efs.Open(embedPath)
	if err != nil {
		return errors.Wrapf(err, "failed to open embedfs file '%s'", embedPath)
	}

	return b.AddFile(f, targetPath)
}

func (b *TarBuilder) AddEmbedDir(efs *embed.FS, embedPath, targetPath string) error {
	dirData, err := efs.ReadDir(embedPath)
	if err != nil {
		return errors.Wrap(err, "failed to read embedfs dir")
	}

	for _, fileData := range dirData {
		filePath := path.Join(embedPath, fileData.Name())

		suffixPath := filePath[len(embedPath)+1:]
		targetFilePath := filepath.Join(targetPath, suffixPath)

		if fileData.IsDir() {
			b.AddEmbedDir(efs, filePath, targetFilePath)
		} else {
			b.AddEmbedFile(efs, filePath, targetFilePath)
		}
	}

	return nil
}

func (b *TarBuilder) Close() error {
	return b.tw.Close()
}
