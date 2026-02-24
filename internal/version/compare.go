// Package semver provides semantic version parsing and comparison helpers.
package semver

import (
	"strconv"
	"strings"
)

type semVersion struct {
	major      int
	minor      int
	patch      int
	prerelease []string
}

// Compare compares two semantic versions.
// Returns: 1 if v1 > v2, -1 if v1 < v2, 0 if equal.
func Compare(v1, v2 string) int {
	left := parse(v1)
	right := parse(v2)

	if left.major != right.major {
		if left.major > right.major {
			return 1
		}
		return -1
	}
	if left.minor != right.minor {
		if left.minor > right.minor {
			return 1
		}
		return -1
	}
	if left.patch != right.patch {
		if left.patch > right.patch {
			return 1
		}
		return -1
	}

	return comparePrerelease(left.prerelease, right.prerelease)
}

func parse(input string) semVersion {
	normalized := strings.TrimSpace(strings.TrimPrefix(input, "v"))
	if i := strings.Index(normalized, "+"); i >= 0 {
		normalized = normalized[:i]
	}

	core := normalized
	prerelease := ""
	if i := strings.Index(core, "-"); i >= 0 {
		prerelease = core[i+1:]
		core = core[:i]
	}

	parts := strings.Split(core, ".")
	version := semVersion{}
	if len(parts) > 0 {
		version.major = parseNumericPart(parts[0])
	}
	if len(parts) > 1 {
		version.minor = parseNumericPart(parts[1])
	}
	if len(parts) > 2 {
		version.patch = parseNumericPart(parts[2])
	}
	if prerelease != "" {
		version.prerelease = strings.Split(prerelease, ".")
	}

	return version
}

func parseNumericPart(part string) int {
	value, err := strconv.Atoi(part)
	if err != nil {
		return 0
	}
	return value
}

func comparePrerelease(left, right []string) int {
	if len(left) == 0 && len(right) == 0 {
		return 0
	}
	if len(left) == 0 {
		return 1
	}
	if len(right) == 0 {
		return -1
	}

	maxLen := len(left)
	if len(right) > maxLen {
		maxLen = len(right)
	}

	for i := 0; i < maxLen; i++ {
		if i >= len(left) {
			return -1
		}
		if i >= len(right) {
			return 1
		}

		cmp := compareIdentifier(left[i], right[i])
		if cmp != 0 {
			return cmp
		}
	}

	return 0
}

func compareIdentifier(left, right string) int {
	leftNum, leftIsNum := parseNumericIdentifier(left)
	rightNum, rightIsNum := parseNumericIdentifier(right)

	if leftIsNum && rightIsNum {
		switch {
		case leftNum > rightNum:
			return 1
		case leftNum < rightNum:
			return -1
		default:
			return 0
		}
	}
	if leftIsNum && !rightIsNum {
		return -1
	}
	if !leftIsNum && rightIsNum {
		return 1
	}

	switch strings.Compare(left, right) {
	case 1:
		return 1
	case -1:
		return -1
	default:
		return 0
	}
}

func parseNumericIdentifier(identifier string) (int, bool) {
	if identifier == "" {
		return 0, false
	}
	for _, char := range identifier {
		if char < '0' || char > '9' {
			return 0, false
		}
	}
	value, err := strconv.Atoi(identifier)
	if err != nil {
		return 0, false
	}
	return value, true
}
