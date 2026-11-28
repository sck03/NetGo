package model

import (
	"regexp"
	"strconv"
	"strings"
)

var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z.-]+))?(?:\+([0-9A-Za-z.-]+))?$`)

// ValidVersion matches scripts/build_config.py, including the native version
// fields' 16-bit limit. Release directory names preserve SemVer build metadata.
func ValidVersion(value string) bool {
	if len(value) > 64 {
		return false
	}
	parts := versionPattern.FindStringSubmatch(value)
	if parts == nil {
		return false
	}
	for _, v := range parts[1:4] {
		if _, err := strconv.ParseUint(v, 10, 16); err != nil {
			return false
		}
	}
	for i, suffix := range parts[4:] {
		if suffix == "" {
			continue
		}
		for _, id := range strings.Split(suffix, ".") {
			if id == "" {
				return false
			}
			if i == 0 && len(id) > 1 && id[0] == '0' && strings.Trim(id, "0123456789") == "" {
				return false
			}
		}
	}
	return true
}
