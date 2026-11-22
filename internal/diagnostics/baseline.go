package diagnostics

import "strings"

func Debian13Release(data string) bool {
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
	return values["ID"] == "debian" && strings.Split(values["VERSION_ID"], ".")[0] == "13"
}
