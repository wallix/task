package version

import (
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVersionMatchesChangelog(t *testing.T) {
	t.Parallel()
	changelog, err := os.ReadFile("../../CHANGELOG.md")
	require.NoError(t, err)

	// Extract first version from "## v3.54.0 - 2026-03-14" style header
	re := regexp.MustCompile(`## v(\d+\.\d+\.\d+)`)
	match := re.FindSubmatch(changelog)
	require.NotNil(t, match, "no version header found in CHANGELOG.md")

	changelogVersion := string(match[1])
	assert.Equal(t, changelogVersion, GetVersion(),
		"version.txt (%s) does not match latest CHANGELOG entry (%s)",
		GetVersion(), changelogVersion)
}
