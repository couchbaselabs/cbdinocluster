package buildversion

import "runtime/debug"

var MainPkgVersion string

func getBuildSetting(info *debug.BuildInfo, key string) string {
	for _, setting := range info.Settings {
		if setting.Key == key {
			return setting.Value
		}
	}
	return ""
}

func GetVersion(pkg string) string {
	buildInfo, ok := debug.ReadBuildInfo()
	if !ok {
		return "nobuilddata"
	}

	if buildInfo.Main.Path == pkg {
		if MainPkgVersion != "" {
			return MainPkgVersion
		}

		buildVersion := buildInfo.Main.Version
		if buildVersion == "(devel)" {
			vcsRevision := getBuildSetting(buildInfo, "vcs.revision")
			vcsModified := getBuildSetting(buildInfo, "vcs.modified") == "true"
			if vcsRevision != "" {
				if vcsModified {
					buildVersion = vcsRevision + "+local"
				} else {
					buildVersion = vcsRevision
				}
			} else {
				buildVersion = "devel"
			}
		}
		return buildVersion
	}

	for _, dep := range buildInfo.Deps {
		if dep.Path == pkg {
			if dep.Replace != nil {
				return dep.Replace.Version
			}
			return dep.Version
		}
	}

	return "notfound"
}
