package config

import "runtime/debug"

// modulePath is the HyperChi module, looked up in the running binary's build
// information to report the framework version.
const modulePath = "github.com/regiellis/hyperchi"

// develVersion is reported when no released module version is available: a
// build of the framework's own checkout, or a replace directive pointing at
// one. It matches the Go toolchain's own placeholder.
const develVersion = "(devel)"

// FrameworkVersion returns the HyperChi module version compiled into the
// running binary, such as "v0.2.0", from debug.ReadBuildInfo. The health
// endpoints and the logging package report it. A build without a released
// version returns "(devel)".
func FrameworkVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return develVersion
	}
	return frameworkVersionFrom(info)
}

// frameworkVersionFrom finds the HyperChi version in info: the dependency's
// version, following a replace directive, or the main module's version when
// the binary is HyperChi itself.
func frameworkVersionFrom(info *debug.BuildInfo) string {
	for _, dep := range info.Deps {
		if dep.Path != modulePath {
			continue
		}
		if dep.Replace != nil {
			dep = dep.Replace
		}
		if dep.Version != "" {
			return dep.Version
		}
		return develVersion
	}
	if info.Main.Path == modulePath && info.Main.Version != "" {
		return info.Main.Version
	}
	return develVersion
}
