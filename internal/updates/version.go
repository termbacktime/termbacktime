package updates

import (
	"regexp"
	"strings"
)

var versionPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

func ValidVersion(value string) bool {
	parts := versionPattern.FindStringSubmatch(value)
	if parts == nil || len(value) > 128 {
		return false
	}
	for _, id := range strings.Split(parts[4], ".") {
		if numeric(id) && len(id) > 1 && id[0] == '0' {
			return false
		}
	}
	return true
}

func numeric(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}

func compareNumber(a, b string) int {
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return strings.Compare(a, b)
}

// Newer compares SemVer precedence, including numeric prerelease identifiers.
func Newer(candidate, installed string) bool {
	if !ValidVersion(candidate) || !ValidVersion(installed) {
		return false
	}
	a, b := versionPattern.FindStringSubmatch(candidate), versionPattern.FindStringSubmatch(installed)
	for i := 1; i <= 3; i++ {
		if cmp := compareNumber(a[i], b[i]); cmp != 0 {
			return cmp > 0
		}
	}
	if a[4] == "" || b[4] == "" {
		return a[4] == "" && b[4] != ""
	}
	x, y := strings.Split(a[4], "."), strings.Split(b[4], ".")
	for i := 0; i < len(x) && i < len(y); i++ {
		cmp := strings.Compare(x[i], y[i])
		if numeric(x[i]) && numeric(y[i]) {
			cmp = compareNumber(x[i], y[i])
		} else if numeric(x[i]) != numeric(y[i]) {
			if numeric(x[i]) {
				cmp = -1
			} else {
				cmp = 1
			}
		}
		if cmp != 0 {
			return cmp > 0
		}
	}
	return len(x) > len(y)
}
