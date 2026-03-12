package fingerprint

func NewSourcesChecker(tempDir string, dry bool) SourcesCheckable {
	return NewChecksumChecker(tempDir, dry)
}
