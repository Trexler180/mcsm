package install

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Detection is a best-effort read of an existing server directory's settings, so
// the panel can import a server already on disk and run it as-is rather than
// re-provisioning over it. Every field is a hint the user confirms in the import
// dialog; nothing here mutates the directory.
type Detection struct {
	Platform    string `json:"platform"`
	MCVersion   string `json:"mc_version"`
	JarFile     string `json:"jar_file"`
	Port        int    `json:"port"`
	HasWorld    bool   `json:"has_world"`
	EULA        bool   `json:"eula_accepted"`
	ModCount    int    `json:"mod_count"`
	PluginCount int    `json:"plugin_count"`
	RuntimeFile bool   `json:"has_runtime_file"`
}

var versionRe = regexp.MustCompile(`(\d+\.\d+(?:\.\d+)?)`)

// Detect inspects dir read-only and returns what it can determine. It never
// writes, so it's safe to run against a live or unmanaged server directory.
func Detect(dir string) Detection {
	d := Detection{Port: 25565}

	entries, _ := os.ReadDir(dir)
	jars := make([]string, 0, 4)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(strings.ToLower(name), ".jar") {
			jars = append(jars, name)
		}
	}

	d.Platform, d.JarFile = detectPlatformJar(dir, jars)
	if d.JarFile != "" {
		if m := versionRe.FindString(d.JarFile); m != "" {
			d.MCVersion = m
		}
	}

	if _, err := os.Stat(filepath.Join(dir, RuntimeFile)); err == nil {
		d.RuntimeFile = true
	}

	props := readProperties(filepath.Join(dir, "server.properties"))
	if p, ok := props["server-port"]; ok {
		if n, err := strconv.Atoi(strings.TrimSpace(p)); err == nil && n > 0 {
			d.Port = n
		}
	}
	level := strings.TrimSpace(props["level-name"])
	if level == "" {
		level = "world"
	}
	if _, err := os.Stat(filepath.Join(dir, level, "level.dat")); err == nil {
		d.HasWorld = true
	}

	if eula := readProperties(filepath.Join(dir, "eula.txt")); strings.EqualFold(strings.TrimSpace(eula["eula"]), "true") {
		d.EULA = true
	}

	d.ModCount = countJars(filepath.Join(dir, "mods"))
	d.PluginCount = countJars(filepath.Join(dir, "plugins"))

	return d
}

// detectPlatformJar picks the runnable server jar and infers the platform from
// filenames and on-disk markers. Returns ("", "") when nothing recognizable is
// present (e.g. a Forge install whose launcher is a run script).
func detectPlatformJar(dir string, jars []string) (platform, jar string) {
	lower := func(s string) string { return strings.ToLower(s) }

	// Loader launchers are unambiguous when present.
	for _, j := range jars {
		switch lower(j) {
		case "fabric-server-launch.jar", "fabric-server-launcher.jar":
			return "fabric", j
		case "quilt-server-launch.jar", "quilt-server-launcher.jar":
			return "quilt", j
		}
	}
	// Named distribution jars carry their flavor in the filename.
	for _, j := range jars {
		l := lower(j)
		switch {
		case strings.HasPrefix(l, "paper"):
			return "paper", j
		case strings.HasPrefix(l, "purpur"):
			return "purpur", j
		case strings.HasPrefix(l, "spigot"):
			return "spigot", j
		case strings.HasPrefix(l, "minecraft_server"):
			return "vanilla", j
		}
	}
	// Modloaders that boot from a run script / args file rather than a jar.
	if dirExists(filepath.Join(dir, "libraries", "net", "neoforged")) {
		return "neoforge", ""
	}
	if dirExists(filepath.Join(dir, "libraries", "net", "minecraftforge")) {
		return "forge", ""
	}

	// Generic launcher jar (often renamed to server.jar) — disambiguate by the
	// config files each flavor leaves behind, which survive even with no plugins.
	serverJar := ""
	for _, j := range jars {
		if lower(j) == "server.jar" {
			serverJar = j
			break
		}
	}
	if serverJar == "" && len(jars) == 1 {
		serverJar = jars[0]
	}
	if serverJar == "" {
		return "", ""
	}
	switch {
	case fileExists(dir, "purpur.yml"):
		return "purpur", serverJar
	case fileExists(dir, filepath.Join("config", "paper-global.yml")),
		fileExists(dir, "paper.yml"):
		return "paper", serverJar
	case fileExists(dir, "spigot.yml"):
		return "spigot", serverJar
	case fileExists(dir, "bukkit.yml"):
		// Bukkit-family but no flavor marker; Paper is the modern default.
		return "paper", serverJar
	}
	// A mods dir with actual jars implies a modloader (Fabric is the common one);
	// an empty mods dir does not.
	if countJars(filepath.Join(dir, "mods")) > 0 {
		return "fabric", serverJar
	}
	return "vanilla", serverJar
}

func fileExists(dir, rel string) bool {
	info, err := os.Stat(filepath.Join(dir, rel))
	return err == nil && !info.IsDir()
}

func readProperties(path string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = v
	}
	return out
}

func countJars(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		l := strings.ToLower(e.Name())
		if strings.HasSuffix(l, ".jar") || strings.HasSuffix(l, ".jar.disabled") {
			n++
		}
	}
	return n
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// LooksLikeServer reports whether dir plausibly holds a Minecraft server, so the
// import scan only surfaces real candidates and not unrelated folders.
func LooksLikeServer(dir string) bool {
	for _, marker := range []string{"server.properties", "eula.txt", "mods", "plugins", RuntimeFile} {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return true
		}
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".jar") {
			return true
		}
	}
	return false
}
