//go:build !linux

package diagnostics

func hostResources(string) (HostResources, error) { return HostResources{}, errUnsupportedResources }
