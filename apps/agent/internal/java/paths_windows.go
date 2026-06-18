package java

func commonPaths() []string {
	return []string{
		`C:\Program Files\Java\jdk-21\bin\java.exe`,
		`C:\Program Files\Java\jdk-17\bin\java.exe`,
		`C:\Program Files\Eclipse Adoptium\jdk-21.0.5.11-hotspot\bin\java.exe`,
		`C:\Program Files\Eclipse Adoptium\jdk-17.0.13.11-hotspot\bin\java.exe`,
	}
}

// commonGlobs matches the per-vendor install layouts so new/unknown versions are
// found without hardcoding exact build numbers.
func commonGlobs() []string {
	return []string{
		`C:\Program Files\Java\*\bin\java.exe`,
		`C:\Program Files\Eclipse Adoptium\*\bin\java.exe`,
		`C:\Program Files\Microsoft\jdk-*\bin\java.exe`,
		`C:\Program Files\Amazon Corretto\*\bin\java.exe`,
		`C:\Program Files\Zulu\*\bin\java.exe`,
		`C:\Program Files\BellSoft\*\bin\java.exe`,
		`C:\Program Files (x86)\Java\*\bin\java.exe`,
	}
}
