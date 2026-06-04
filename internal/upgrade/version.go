package upgrade

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type Channel string

const (
	StableChannel  Channel = "stable"
	NightlyChannel Channel = "nightly"
)

type Version struct {
	Raw     string
	Major   int
	Minor   int
	Patch   int
	Pre     string
	Present bool
}

const versionPattern = `([0-9]+)\.([0-9]+)\.([0-9]+)(?:-([0-9A-Za-z][0-9A-Za-z.-]*))?`

var (
	exactVersionRE = regexp.MustCompile(`^v?` + versionPattern + `$`)
	// Matches the leading line of `entire --version` ("Entire CLI x.y.z") and
	// `git-remote-entire --version` ("git-remote-entire x.y.z"). git-remote-entire
	// precedes entire in the alternation so it isn't shadowed.
	entireVersionLineRE = regexp.MustCompile(`(?m)^(?:Entire CLI|git-remote-entire|entire)\s+v?` + versionPattern + `(?:\s|$)`)
)

func ParseVersion(s string) (Version, error) {
	match := exactVersionRE.FindStringSubmatch(strings.TrimSpace(s))
	if match == nil {
		match = entireVersionLineRE.FindStringSubmatch(s)
	}
	if match == nil {
		return Version{}, fmt.Errorf("could not parse version from %q", s)
	}
	return parseVersionMatch(match)
}

func parseVersionMatch(match []string) (Version, error) {
	major, err := strconv.Atoi(match[1])
	if err != nil {
		return Version{}, fmt.Errorf("parse major version: %w", err)
	}
	minor, err := strconv.Atoi(match[2])
	if err != nil {
		return Version{}, fmt.Errorf("parse minor version: %w", err)
	}
	patch, err := strconv.Atoi(match[3])
	if err != nil {
		return Version{}, fmt.Errorf("parse patch version: %w", err)
	}

	raw := fmt.Sprintf("%d.%d.%d", major, minor, patch)
	if match[4] != "" {
		raw += "-" + match[4]
	}

	return Version{
		Raw:     raw,
		Major:   major,
		Minor:   minor,
		Patch:   patch,
		Pre:     match[4],
		Present: true,
	}, nil
}

func (v Version) String() string {
	if !v.Present {
		return ""
	}
	return v.Raw
}

func (v Version) Tag() string {
	if !v.Present {
		return ""
	}
	return "v" + v.Raw
}

func (v Version) IsNightly() bool {
	return strings.HasPrefix(v.Pre, "nightly.")
}

func (v Version) Compare(other Version) int {
	for _, pair := range [][2]int{
		{v.Major, other.Major},
		{v.Minor, other.Minor},
		{v.Patch, other.Patch},
	} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}

	if v.Pre == "" && other.Pre == "" {
		return 0
	}
	if v.Pre == "" {
		return 1
	}
	if other.Pre == "" {
		return -1
	}
	return comparePrerelease(v.Pre, other.Pre)
}

func comparePrerelease(a, b string) int {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")
	for i := 0; i < len(aParts) || i < len(bParts); i++ {
		if i >= len(aParts) {
			return -1
		}
		if i >= len(bParts) {
			return 1
		}

		cmp := comparePrereleaseIdentifier(aParts[i], bParts[i])
		if cmp != 0 {
			return cmp
		}
	}
	return 0
}

func comparePrereleaseIdentifier(a, b string) int {
	aNum, aErr := strconv.Atoi(a)
	bNum, bErr := strconv.Atoi(b)

	if aErr == nil && bErr == nil {
		if aNum < bNum {
			return -1
		}
		if aNum > bNum {
			return 1
		}
		return 0
	}
	if aErr == nil {
		return -1
	}
	if bErr == nil {
		return 1
	}

	return strings.Compare(a, b)
}
