package process

import "testing"

const javaVersionSentence = "UnsupportedClassVersionError: x has been compiled by a more recent version of the Java Runtime (class file version 69.0), this version of the Java Runtime only recognizes class file versions up to 65.0"

// A player who types a diagnostic sentence into chat must not be able to make
// the agent kill a running server.
func TestChatCannotTriggerStartupDetectors(t *testing.T) {
	inst := newInstance(t.TempDir(), "srv", StartConfig{})
	inst.mu.Lock()
	inst.status = StatusOnline
	inst.mu.Unlock()

	inst.consumeLine("[12:34:56] [Async Chat Thread - #0/INFO]: <Steve> "+javaVersionSentence, "stdout")
	inst.consumeLine("[12:34:57] [Server thread/INFO]: "+javaVersionSentence, "stdout")

	if got := inst.statusInfo().Status; got != StatusOnline {
		t.Fatalf("status = %s; text in the log of a running server must not flip it", got)
	}
	if inst.Conflict() != nil {
		t.Fatal("a conflict was recorded for a running server")
	}
}

// While starting, a real diagnostic is still caught — but not one typed in
// chat, which a starting server can't show anyway.
func TestStartupDetectorsStillWorkWhileStarting(t *testing.T) {
	inst := newInstance(t.TempDir(), "srv", StartConfig{})
	inst.consumeLine("[12:00:00] [Server thread/INFO]: <Steve> "+javaVersionSentence, "stdout")
	if inst.Conflict() != nil {
		t.Fatal("a chat line was read as a diagnostic")
	}
	inst.consumeLine("Caused by: java.lang."+javaVersionSentence, "stderr")
	if inst.Conflict() == nil || inst.statusInfo().Status != StatusStartupFailure {
		t.Fatal("a genuine startup diagnostic was missed")
	}
}

func TestIsChatLine(t *testing.T) {
	for line, want := range map[string]bool{
		"[12:00:00] [Server thread/INFO]: <Steve> hi":                       true,
		"[12:00:00] [Async Chat Thread - #0/INFO]: [Not Secure] <Alex> yo": true,
		"[12:00:00] [Server thread/INFO]: Steve joined the game":            false,
		"[12:00:00] [main/ERROR]: Mixin apply for mod foo failed":           false,
	} {
		if got := isChatLine(line); got != want {
			t.Errorf("isChatLine(%q) = %v, want %v", line, got, want)
		}
	}
}
