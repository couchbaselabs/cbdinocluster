package localdeploy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"runtime"
	"strings"

	"github.com/brett19/cbdyncluster2/clustercontrol"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

const (
	CB_INSTALLER_PATH string = "/tmp/cbinstallers"
)

type ServerDef struct {
	Version             string
	BuildNo             int
	UseCommunityEdition bool
	UseServerless       bool
}

type OsxController struct {
	Logger *zap.Logger
}

func (c *OsxController) cleanupPrevious(ctx context.Context) error {
	// unmount any existing Couchbase volumes
	mounts, err := os.ReadDir("/Volumes/")
	if err != nil {
		return errors.Wrap(err, "failed to list mounts")
	}
	for _, mount := range mounts {
		if strings.HasPrefix(mount.Name(), "Couchbase") {
			err = execAndPipe(c.Logger, "hdiutil", "detach", "/Volumes/"+mount.Name())
			if err != nil {
				return errors.Wrap(err, "failed to detach existing volume")
			}
		}
	}

	// try and remove the applications folder
	err = os.RemoveAll("/Applications/Couchbase Server.app")
	if err != nil {
		return errors.Wrap(err, "failed to remove existing app file")
	}

	return nil
}

func (c *OsxController) Start(ctx context.Context, def *ServerDef) error {
	if def.BuildNo != 0 {
		return errors.New("only ga releases are currently supported")
	}
	if def.UseServerless {
		return errors.New("serverless is not currently supported")
	}

	archTag := ""
	if runtime.GOARCH == "amd64" {
		archTag = "x86_64"
	} else if runtime.GOARCH == "arm" {
		archTag = "arm64"
	} else {
		return errors.New("unsupported architecture")
	}

	entComTag := "enterprise"
	if def.UseCommunityEdition {
		entComTag = "community"
	}

	installerName := fmt.Sprintf("couchbase-server-%s_%s-macos_%s.dmg", entComTag, def.Version, archTag)
	installerUrl := fmt.Sprintf("https://packages.couchbase.com/releases/%s/%s", def.Version, installerName)
	installerPath := path.Join(CB_INSTALLER_PATH, installerName)

	err := c.cleanupPrevious(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to cleanup previous running server")
	}

	if _, err := os.Stat(installerPath); err == nil {
		c.Logger.Debug("found installer on disk")
		// file already exists
	} else {
		c.Logger.Debug("downloading installer")

		err := os.MkdirAll(CB_INSTALLER_PATH, os.ModePerm)
		if err != nil {
			return errors.Wrap(err, "failed to create installers path")
		}

		out, err := os.Create(installerPath)
		if err != nil {
			return errors.Wrap(err, "failed to create installer output file")
		}
		defer out.Close()

		resp, err := http.Get(installerUrl)
		if err != nil {
			return errors.Wrap(err, "failed to fetch installer via http")
		}
		defer resp.Body.Close()

		n, err := io.Copy(out, resp.Body)
		if err != nil {
			return errors.Wrap(err, "failed to download installer")
		}
		c.Logger.Debug("downloaded installer", zap.Int64("size", n))
	}

	// hdiutil attach couchbase-server-....dmg
	err = execAndPipe(c.Logger, "hdiutil", "attach", installerPath)
	if err != nil {
		// TODO(brett19): Ignored for now...
		return errors.Wrap(err, "failed to mount volume")
	}

	mountName := ""
	mounts, err := os.ReadDir("/Volumes/")
	for _, mount := range mounts {
		if strings.HasPrefix(mount.Name(), "Couchbase") {
			mountName = mount.Name()
		}
	}
	if mountName == "" {
		return errors.Wrap(err, "failed to find mounted volume")
	}

	appFile := ""
	mountFiles, err := os.ReadDir("/Volumes/" + mountName)
	for _, mountFile := range mountFiles {
		if strings.HasSuffix(mountFile.Name(), "app") {
			appFile = mountFile.Name()
		}
	}
	if appFile == "" {
		return errors.Wrap(err, "failed to find app in volume")
	}

	// copy to Applications folder
	err = execAndPipe(c.Logger, "cp", "-R", "/Volumes/"+mountName+"/"+appFile, "/Applications")
	if err != nil {
		return errors.Wrap(err, "failed to copy app file")
	}

	// hdiutil detach /Volumes/Couchbase*
	err = execAndPipe(c.Logger, "hdiutil", "detach", "/Volumes/"+mountName)
	if err != nil {
		return errors.Wrap(err, "failed to detach volume")
	}

	// maybe dequarantine it if we an
	// _ = execAndPipe("xattr", "-d", "-r", "com.apple.quarantine", "/Applications/"+appFile)

	// execute
	err = execAndPipe(c.Logger, "open", "-a", "/Applications/"+appFile)
	if err != nil {
		return errors.Wrap(err, "failed to launch server")
	}

	// wait till it's ready
	clusterCtrl := &clustercontrol.NodeManager{
		Endpoint: fmt.Sprintf("http://%s:%d", "127.0.0.1", 8091),
	}

	err = clusterCtrl.WaitForOnline(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to wait for node readiness")
	}

	return nil
}

func (c *OsxController) Stop(ctx context.Context) error {
	_ = execAndPipe(c.Logger, "osascript", "-e", "quit app \"Couchbase Server\"")

	return nil
}
