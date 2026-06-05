// Package install fetches platform server runtimes (Paper, Purpur, Vanilla,
// Fabric, Quilt, Spigot, Forge, NeoForge) into a server directory so the user
// doesn't have to do it manually before first start.
//
// For "simple" platforms we drop a server.jar in the directory.
// For installer-based platforms (Forge, NeoForge) we run the installer and
// write `mcsm-runtime.txt` containing the exact post-`java` argument list,
// which the agent reads at start instead of `-jar server.jar`.
package install

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strings"
	"time"
)

const (
	JarName     = "server.jar"
	RuntimeFile = "mcsm-runtime.txt"
)

var httpClient = &http.Client{Timeout: 15 * time.Minute}

// Reinstall removes existing runtime artifacts and re-fetches them, so a changed
// Minecraft/loader version actually takes effect (EnsureRuntime alone is a no-op
// when a jar is already present).
func Reinstall(ctx context.Context, dir, platform, mcVersion, javaBinary string) error {
	_ = os.Remove(filepath.Join(dir, JarName))
	_ = os.Remove(filepath.Join(dir, RuntimeFile))
	return EnsureRuntime(ctx, dir, platform, mcVersion, javaBinary)
}

// EnsureRuntime makes the server runnable. Idempotent: returns nil if
// `server.jar` or `mcsm-runtime.txt` already exists in dir.
//
// javaBinary is the absolute path or shell name used to spawn installer JARs
// (Forge/NeoForge/Spigot). Pass "java" to fall back to PATH lookup.
func EnsureRuntime(ctx context.Context, dir, platform, mcVersion, javaBinary string) error {
	if _, err := os.Stat(filepath.Join(dir, JarName)); err == nil {
		return nil
	}
	if _, err := os.Stat(filepath.Join(dir, RuntimeFile)); err == nil {
		return nil
	}
	if mcVersion == "" {
		return fmt.Errorf("mc_version required to auto-install %s", platform)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	if javaBinary == "" {
		javaBinary = "java"
	}

	switch strings.ToLower(platform) {
	case "paper":
		return paperJar(ctx, dir, mcVersion)
	case "purpur":
		return purpurJar(ctx, dir, mcVersion)
	case "vanilla":
		return vanillaJar(ctx, dir, mcVersion)
	case "fabric":
		return fabricJar(ctx, dir, mcVersion)
	case "quilt":
		return quiltJar(ctx, dir, mcVersion)
	case "spigot":
		return spigotJar(ctx, dir, mcVersion, javaBinary)
	case "forge":
		return forgeInstall(ctx, dir, mcVersion, javaBinary)
	case "neoforge":
		return neoforgeInstall(ctx, dir, mcVersion, javaBinary)
	default:
		return fmt.Errorf("unknown platform %q", platform)
	}
}

// ── Paper ────────────────────────────────────────────────────────────────────

func paperJar(ctx context.Context, dir, mcVersion string) error {
	var builds []paperFillBuild
	if err := getJSON(ctx, fmt.Sprintf(
		"https://fill.papermc.io/v3/projects/paper/versions/%s/builds", mcVersion), &builds); err != nil {
		return fmt.Errorf("paper version lookup: %w", err)
	}
	url, ok := selectPaperDownloadURL(builds)
	if !ok {
		return fmt.Errorf("paper has no downloadable build for mc %s", mcVersion)
	}
	return download(ctx, url, filepath.Join(dir, JarName))
}

type paperFillDownload struct {
	URL string `json:"url"`
}

type paperFillBuild struct {
	ID        int                          `json:"id"`
	Channel   string                       `json:"channel"`
	Downloads map[string]paperFillDownload `json:"downloads"`
}

func (b paperFillBuild) buildChannel() string { return b.Channel }
func (b paperFillBuild) buildID() int         { return b.ID }
func (b paperFillBuild) buildDownloadURL() string {
	if b.Downloads == nil {
		return ""
	}
	return b.Downloads["server:default"].URL
}

type paperDownloadBuild interface {
	buildChannel() string
	buildID() int
	buildDownloadURL() string
}

func selectPaperDownloadURL[T paperDownloadBuild](builds []T) (string, bool) {
	bestPriority := -1
	bestID := -1
	var bestURL string
	for _, build := range builds {
		url := build.buildDownloadURL()
		if url == "" {
			continue
		}
		priority := paperChannelPriority(build.buildChannel())
		if priority > bestPriority || (priority == bestPriority && build.buildID() > bestID) {
			bestPriority = priority
			bestID = build.buildID()
			bestURL = url
		}
	}
	return bestURL, bestURL != ""
}

func paperChannelPriority(channel string) int {
	switch strings.ToLower(channel) {
	case "recommended":
		return 3
	case "stable":
		return 2
	case "beta":
		return 1
	default:
		return 0
	}
}

// ── Purpur ───────────────────────────────────────────────────────────────────

func purpurJar(ctx context.Context, dir, mcVersion string) error {
	url := fmt.Sprintf("https://api.purpurmc.org/v2/purpur/%s/latest/download", mcVersion)
	return download(ctx, url, filepath.Join(dir, JarName))
}

// ── Vanilla ──────────────────────────────────────────────────────────────────

func vanillaJar(ctx context.Context, dir, mcVersion string) error {
	type manifestEntry struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	type manifest struct {
		Versions []manifestEntry `json:"versions"`
	}
	type versionInfo struct {
		Downloads struct {
			Server struct {
				URL string `json:"url"`
			} `json:"server"`
		} `json:"downloads"`
	}

	var m manifest
	if err := getJSON(ctx,
		"https://piston-meta.mojang.com/mc/game/version_manifest_v2.json", &m); err != nil {
		return fmt.Errorf("vanilla manifest: %w", err)
	}
	var versionURL string
	for _, v := range m.Versions {
		if v.ID == mcVersion {
			versionURL = v.URL
			break
		}
	}
	if versionURL == "" {
		return fmt.Errorf("vanilla version %s not in manifest", mcVersion)
	}

	var info versionInfo
	if err := getJSON(ctx, versionURL, &info); err != nil {
		return fmt.Errorf("vanilla version info: %w", err)
	}
	if info.Downloads.Server.URL == "" {
		return fmt.Errorf("vanilla %s has no server download", mcVersion)
	}
	return download(ctx, info.Downloads.Server.URL, filepath.Join(dir, JarName))
}

// ── Fabric ───────────────────────────────────────────────────────────────────

func fabricJar(ctx context.Context, dir, mcVersion string) error {
	type loaderEntry struct {
		Loader struct {
			Version string `json:"version"`
			Stable  bool   `json:"stable"`
		} `json:"loader"`
	}
	type installerEntry struct {
		Version string `json:"version"`
		Stable  bool   `json:"stable"`
	}

	var loaders []loaderEntry
	if err := getJSON(ctx, fmt.Sprintf(
		"https://meta.fabricmc.net/v2/versions/loader/%s", mcVersion), &loaders); err != nil {
		return fmt.Errorf("fabric loader lookup: %w", err)
	}
	if len(loaders) == 0 {
		return fmt.Errorf("no fabric loader for mc %s", mcVersion)
	}
	loader := loaders[0].Loader.Version
	for _, l := range loaders {
		if l.Loader.Stable {
			loader = l.Loader.Version
			break
		}
	}

	var installers []installerEntry
	if err := getJSON(ctx,
		"https://meta.fabricmc.net/v2/versions/installer", &installers); err != nil {
		return fmt.Errorf("fabric installer lookup: %w", err)
	}
	if len(installers) == 0 {
		return fmt.Errorf("no fabric installer available")
	}
	installer := installers[0].Version
	for _, i := range installers {
		if i.Stable {
			installer = i.Version
			break
		}
	}

	url := fmt.Sprintf("https://meta.fabricmc.net/v2/versions/loader/%s/%s/%s/server/jar",
		mcVersion, loader, installer)
	return download(ctx, url, filepath.Join(dir, JarName))
}

// ── Quilt ────────────────────────────────────────────────────────────────────

func quiltJar(ctx context.Context, dir, mcVersion string) error {
	type loaderEntry struct {
		Loader struct {
			Version string `json:"version"`
		} `json:"loader"`
	}
	type installerEntry struct {
		Version string `json:"version"`
	}

	var loaders []loaderEntry
	if err := getJSON(ctx, fmt.Sprintf(
		"https://meta.quiltmc.org/v3/versions/loader/%s", mcVersion), &loaders); err != nil {
		return fmt.Errorf("quilt loader lookup: %w", err)
	}
	if len(loaders) == 0 {
		return fmt.Errorf("no quilt loader for mc %s", mcVersion)
	}
	loader := loaders[0].Loader.Version

	var installers []installerEntry
	if err := getJSON(ctx,
		"https://meta.quiltmc.org/v3/versions/installer", &installers); err != nil {
		return fmt.Errorf("quilt installer lookup: %w", err)
	}
	if len(installers) == 0 {
		return fmt.Errorf("no quilt installer available")
	}
	installer := installers[0].Version

	url := fmt.Sprintf("https://meta.quiltmc.org/v3/versions/loader/%s/%s/%s/server/jar",
		mcVersion, loader, installer)
	return download(ctx, url, filepath.Join(dir, JarName))
}

// ── Spigot (BuildTools — slow, ~5–10 min) ────────────────────────────────────

func spigotJar(ctx context.Context, dir, mcVersion, javaBinary string) error {
	tmpDir := filepath.Join(dir, ".buildtools")
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	btJar := filepath.Join(tmpDir, "BuildTools.jar")
	if err := download(ctx,
		"https://hub.spigotmc.org/jenkins/job/BuildTools/lastSuccessfulBuild/artifact/target/BuildTools.jar",
		btJar); err != nil {
		return fmt.Errorf("download BuildTools: %w", err)
	}

	cmd := exec.CommandContext(ctx, javaBinary, "-jar", "BuildTools.jar", "--rev", mcVersion)
	cmd.Dir = tmpDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("BuildTools failed: %w (last output: %s)", err, snippet(out, 500))
	}

	matches, _ := filepath.Glob(filepath.Join(tmpDir, "spigot-*.jar"))
	if len(matches) == 0 {
		return fmt.Errorf("spigot jar not produced; BuildTools tail: %s", snippet(out, 200))
	}
	return replaceFile(matches[0], filepath.Join(dir, JarName))
}

// ── Forge ────────────────────────────────────────────────────────────────────

func forgeInstall(ctx context.Context, dir, mcVersion, javaBinary string) error {
	var promos struct {
		Promos map[string]string `json:"promos"`
	}
	if err := getJSON(ctx,
		"https://files.minecraftforge.net/net/minecraftforge/forge/promotions_slim.json",
		&promos); err != nil {
		return fmt.Errorf("forge promotions: %w", err)
	}
	forgeVer := promos.Promos[mcVersion+"-recommended"]
	if forgeVer == "" {
		forgeVer = promos.Promos[mcVersion+"-latest"]
	}
	if forgeVer == "" {
		return fmt.Errorf("no forge build for mc %s", mcVersion)
	}
	fullVer := mcVersion + "-" + forgeVer

	installerJar := filepath.Join(dir, "forge-installer.jar")
	url := fmt.Sprintf(
		"https://maven.minecraftforge.net/net/minecraftforge/forge/%s/forge-%s-installer.jar",
		fullVer, fullVer)
	if err := download(ctx, url, installerJar); err != nil {
		return fmt.Errorf("download forge installer: %w", err)
	}
	defer os.Remove(installerJar)
	defer os.Remove(installerJar + ".log")

	return runInstallerAndCaptureRuntime(ctx, dir, filepath.Base(installerJar), javaBinary)
}

// ── NeoForge ─────────────────────────────────────────────────────────────────

func neoforgeInstall(ctx context.Context, dir, mcVersion, javaBinary string) error {
	// NeoForge versions look like "21.4.155" for MC "1.21.4". Skip leading
	// "1." and match prefix "<minor>.<patch>." in the maven metadata.
	parts := strings.Split(mcVersion, ".")
	if len(parts) < 2 {
		return fmt.Errorf("invalid mc version %q", mcVersion)
	}
	var prefix string
	if len(parts) >= 3 {
		prefix = fmt.Sprintf("%s.%s.", parts[1], parts[2])
	} else {
		prefix = fmt.Sprintf("%s.0.", parts[1])
	}

	type mavenMeta struct {
		XMLName    xml.Name `xml:"metadata"`
		Versioning struct {
			Versions struct {
				Version []string `xml:"version"`
			} `xml:"versions"`
		} `xml:"versioning"`
	}
	var meta mavenMeta
	if err := getXML(ctx,
		"https://maven.neoforged.net/releases/net/neoforged/neoforge/maven-metadata.xml",
		&meta); err != nil {
		return fmt.Errorf("neoforge metadata: %w", err)
	}

	var latest string
	for _, v := range meta.Versioning.Versions.Version {
		if strings.HasPrefix(v, prefix) {
			latest = v // versions are in ascending order; keep last match
		}
	}
	if latest == "" {
		return fmt.Errorf("no neoforge build for mc %s (prefix %s)", mcVersion, prefix)
	}

	installerJar := filepath.Join(dir, "neoforge-installer.jar")
	url := fmt.Sprintf(
		"https://maven.neoforged.net/releases/net/neoforged/neoforge/%s/neoforge-%s-installer.jar",
		latest, latest)
	if err := download(ctx, url, installerJar); err != nil {
		return fmt.Errorf("download neoforge installer: %w", err)
	}
	defer os.Remove(installerJar)
	defer os.Remove(installerJar + ".log")

	return runInstallerAndCaptureRuntime(ctx, dir, filepath.Base(installerJar), javaBinary)
}

// ── Installer runner shared by Forge / NeoForge ──────────────────────────────

// runInstallerAndCaptureRuntime executes `java -jar <installer> --installServer`
// inside dir, then extracts the launch args from the produced run.bat / run.sh
// and writes them to mcsm-runtime.txt. The agent reads that file at start.
func runInstallerAndCaptureRuntime(ctx context.Context, dir, installerJar, javaBinary string) error {
	cmd := exec.CommandContext(ctx, javaBinary, "-jar", installerJar, "--installServer")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("installer failed: %w (output: %s)", err, snippet(out, 500))
	}

	// Both run.sh and run.bat have a `java <args> "$@"` (or `%*`) line. Read
	// whichever is present — the args are the same on both for modern Forge/NF.
	scripts := []string{"run.sh", "run.bat"}
	if goruntime.GOOS == "windows" {
		scripts = []string{"run.bat", "run.sh"}
	}
	var content string
	for _, name := range scripts {
		if data, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			content = string(data)
			break
		}
	}
	if content == "" {
		return fmt.Errorf("installer did not produce run.sh or run.bat")
	}

	args := extractJavaArgs(content)
	if args == "" {
		return fmt.Errorf("could not parse java args from run script")
	}
	return os.WriteFile(filepath.Join(dir, RuntimeFile), []byte(args), 0644)
}

// Captures everything between `java` and the trailing `"$@"` / `%*` /
// (nothing). Tolerant of leading whitespace and extra @ECHO OFF style noise.
var javaArgsRe = regexp.MustCompile(`(?m)^\s*(?:"[^"]*[\\/]|)java(?:\.exe)?["']?\s+(.+?)(?:\s*"?\$@"?|\s*%\*)?\s*$`)

func extractJavaArgs(scriptContent string) string {
	matches := javaArgsRe.FindStringSubmatch(scriptContent)
	if len(matches) < 2 {
		return ""
	}
	return strings.TrimSpace(matches[1])
}

// ── HTTP helpers ─────────────────────────────────────────────────────────────

func getJSON(ctx context.Context, url string, out any) error {
	resp, err := httpRequest(ctx, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(out)
}

func getXML(ctx context.Context, url string, out any) error {
	resp, err := httpRequest(ctx, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return xml.NewDecoder(resp.Body).Decode(out)
}

func httpRequest(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "mcsm/0.1.0 (https://github.com/Trexler180/mcsm)")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

func download(ctx context.Context, url, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	tmp := dst + ".part"
	_ = os.Remove(tmp)

	resp, err := httpRequest(ctx, url)
	if err != nil {
		return err
	}

	f, err := os.Create(tmp)
	if err != nil {
		resp.Body.Close()
		return err
	}
	_, copyErr := io.Copy(f, resp.Body)
	closeErr := f.Close()
	bodyErr := resp.Body.Close()
	if copyErr != nil {
		os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		os.Remove(tmp)
		return closeErr
	}
	if bodyErr != nil {
		os.Remove(tmp)
		return bodyErr
	}
	if err := replaceFile(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func replaceFile(src, dst string) error {
	var lastErr error
	for attempt := 0; attempt < 6; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt*100) * time.Millisecond)
		}
		if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
			lastErr = err
			continue
		}
		if err := os.Rename(src, dst); err != nil {
			lastErr = err
			continue
		}
		return nil
	}

	// Some Windows filesystems and endpoint scanners can deny same-directory
	// rename even after all handles are closed. A non-atomic copy is still safe
	// here because callers only invoke replaceFile after writing a complete temp
	// file, and we clean up the source once the destination is fully closed.
	if err := copyFile(src, dst); err != nil {
		return fmt.Errorf("rename failed: %w; copy fallback failed: %v", lastErr, err)
	}
	return os.Remove(src)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(dst)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(dst)
		return closeErr
	}
	return nil
}

func snippet(b []byte, n int) string {
	if len(b) > n {
		b = b[len(b)-n:] // tail — installers print useful errors at the end
	}
	return string(b)
}
