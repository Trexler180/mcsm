// Package helperjar embeds the built helper mod so the agent can install it
// without a network round trip.
//
// The jar is produced by apps/mod (`./gradlew build`, whose installToAgent task
// copies it here) and is committed to the repository. That is a deliberate
// trade: a small binary in git history buys a Go build that needs no JDK, no
// Gradle and no network, and an agent that can provision the mod on an
// air-gapped host. CI rebuilds the jar and compares hashes, so a stale copy
// cannot reach main.
package helperjar

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"sync"
)

//go:embed mcsm-helper.jar
var jar []byte

// FileName is the name the jar is written as inside a server's mods directory.
//
// Version-free on purpose: upgrading a server's helper must be an overwrite, not
// an accumulation of stale jars that Fabric would then try to load side by side.
const FileName = "mcsm-helper.jar"

// Metadata describing the embedded build. These must match the jar's own
// fabric.mod.json and its entry in the published index.
//
// TODO: generate this file from the Gradle build so the two cannot drift. Hand
// maintenance is tolerable for one embedded build and stops being so the moment
// there are several.
const (
	// ModVersion is the embedded build's own version.
	ModVersion = "1.0.0"
	// ProtocolVersion is the link protocol this build speaks. It bounds which
	// published builds the agent is willing to install.
	ProtocolVersion = 1
)

// MinecraftVersions are the exact game versions the embedded build supports.
//
// Fabric treats an unsatisfied dependency as fatal, so this list is the floor
// that keeps an offline host from installing a jar onto a server it would break.
var MinecraftVersions = []string{"26.2"}

var (
	sumOnce sync.Once
	sum     string
)

// Bytes returns the embedded jar.
//
// Returns the backing slice rather than a copy — callers write it to disk and
// must not modify it.
func Bytes() []byte {
	return jar
}

// Size is the jar's length in bytes.
func Size() int {
	return len(jar)
}

// SHA256 is the hex digest of the embedded jar, used to decide whether a
// server's installed copy is already current.
func SHA256() string {
	sumOnce.Do(func() {
		digest := sha256.Sum256(jar)
		sum = hex.EncodeToString(digest[:])
	})
	return sum
}
