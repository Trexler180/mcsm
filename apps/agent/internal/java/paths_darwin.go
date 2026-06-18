package java

func commonPaths() []string {
	return []string{
		"/usr/bin/java",
		"/usr/local/bin/java",
		"/opt/homebrew/bin/java",
	}
}

// commonGlobs matches the standard macOS JDK bundle layout plus Homebrew's
// keg-only openjdk locations (Apple Silicon under /opt/homebrew, Intel under
// /usr/local), so any installed version is found without exact names.
func commonGlobs() []string {
	return []string{
		"/Library/Java/JavaVirtualMachines/*/Contents/Home/bin/java",
		"/opt/homebrew/opt/openjdk*/bin/java",
		"/opt/homebrew/Cellar/openjdk*/*/bin/java",
		"/usr/local/opt/openjdk*/bin/java",
		"/usr/local/Cellar/openjdk*/*/bin/java",
	}
}
