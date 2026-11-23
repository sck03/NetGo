package diagnostics

import "strings"

func SupportedDebianRelease(data string) bool {
	values := map[string]string{}
	for _, line := range strings.Split(data, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || (key != "ID" && key != "VERSION_ID") {
			continue
		}
		if _, duplicate := values[key]; duplicate {
			return false
		}
		values[key] = strings.Trim(value, "\"'")
	}
	version := strings.Split(values["VERSION_ID"], ".")[0]
	return values["ID"] == "debian" && (version == "12" || version == "13")
}
