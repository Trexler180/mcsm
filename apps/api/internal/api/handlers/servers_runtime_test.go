package handlers

import (
	"encoding/json"
	"testing"
)

func TestWithoutImportSettingsPreservesUnrelatedSettings(t *testing.T) {
	settings := json.RawMessage(`{"import":{"jar_file":"fabric-server-launch.jar","no_install":true},"motd":"hello"}`)

	got, changed := withoutImportSettings(settings)
	if !changed {
		t.Fatal("import settings were not removed")
	}
	var decoded map[string]any
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if _, ok := decoded["import"]; ok {
		t.Fatal("import settings remain after conversion")
	}
	if decoded["motd"] != "hello" {
		t.Fatalf("unrelated setting = %#v, want hello", decoded["motd"])
	}
}

func TestWithoutImportSettingsLeavesManagedServerUntouched(t *testing.T) {
	settings := json.RawMessage(`{"motd":"hello"}`)
	got, changed := withoutImportSettings(settings)
	if changed {
		t.Fatal("managed settings reported as changed")
	}
	if string(got) != string(settings) {
		t.Fatalf("settings changed: %s", got)
	}
}
