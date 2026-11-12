package main

import "testing"

func TestPlaceholderTokens(t *testing.T) {
	for _, v := range []string{"dev-agent-token", "CHANGE_ME", " changeme "} {
		if !isPlaceholderToken(v) {
			t.Errorf("%q should be refused as a placeholder", v)
		}
	}
	if isPlaceholderToken("9f2c4e1ab07d4d6f8a3b") {
		t.Error("a real token was refused")
	}
}
