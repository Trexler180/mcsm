package java

func commonPaths() []string {
	return []string{
		"/usr/bin/java",
		"/usr/local/bin/java",
		"/usr/lib/jvm/java-21/bin/java",
		"/usr/lib/jvm/java-21-openjdk-amd64/bin/java",
		"/usr/lib/jvm/java-17/bin/java",
		"/usr/lib/jvm/java-17-openjdk-amd64/bin/java",
		"/usr/lib/jvm/java-11/bin/java",
		"/usr/lib/jvm/java-11-openjdk-amd64/bin/java",
		"/usr/lib/jvm/java-8-openjdk-amd64/bin/java",
		"/opt/java/21/bin/java",
		"/opt/java/17/bin/java",
	}
}

// commonGlobs matches the standard distro and tarball JVM layouts so any
// installed version is found without enumerating exact directory names.
func commonGlobs() []string {
	return []string{
		"/usr/lib/jvm/*/bin/java",
		"/usr/lib/jvm/*/jre/bin/java",
		"/opt/java/*/bin/java",
		"/opt/*/bin/java",
		"/usr/lib64/jvm/*/bin/java",
	}
}
