package install

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractJavaArgs(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			"neoforge run.bat (CRLF)",
			"@ECHO OFF\r\njava @user_jvm_args.txt @libraries/net/neoforged/neoforge/21.4.155/win_args.txt %*\r\npause\r\n",
			"@user_jvm_args.txt @libraries/net/neoforged/neoforge/21.4.155/win_args.txt",
		},
		{
			"neoforge run.sh",
			"#!/usr/bin/env sh\njava @user_jvm_args.txt @libraries/net/neoforged/neoforge/21.4.155/unix_args.txt \"$@\"\n",
			"@user_jvm_args.txt @libraries/net/neoforged/neoforge/21.4.155/unix_args.txt",
		},
		{
			"forge run.bat with trailing echo",
			"@echo off\njava @user_jvm_args.txt @libraries/net/minecraftforge/forge/1.21.4-53.0.10/win_args.txt %*\necho Exit %errorlevel%\npause\n",
			"@user_jvm_args.txt @libraries/net/minecraftforge/forge/1.21.4-53.0.10/win_args.txt",
		},
		{
			"no trailing $@ or %*",
			"java @user_jvm_args.txt @libraries/x/y/z/args.txt\n",
			"@user_jvm_args.txt @libraries/x/y/z/args.txt",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractJavaArgs(tc.in)
			if got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestSelectPaperDownloadURL(t *testing.T) {
	builds := []paperFillBuild{
		{
			ID:      12,
			Channel: "stable",
			Downloads: map[string]paperFillDownload{
				"server:default": {URL: "https://example.test/stable-12.jar"},
			},
		},
		{
			ID:      14,
			Channel: "beta",
			Downloads: map[string]paperFillDownload{
				"server:default": {URL: "https://example.test/beta-14.jar"},
			},
		},
		{
			ID:      9,
			Channel: "recommended",
			Downloads: map[string]paperFillDownload{
				"server:default": {URL: "https://example.test/recommended-9.jar"},
			},
		},
	}
	got, ok := selectPaperDownloadURL(builds)
	if !ok {
		t.Fatal("expected a Paper download URL")
	}
	if want := "https://example.test/recommended-9.jar"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSelectPaperDownloadURLUsesNewestWithinChannel(t *testing.T) {
	builds := []paperFillBuild{
		{
			ID:      12,
			Channel: "stable",
			Downloads: map[string]paperFillDownload{
				"server:default": {URL: "https://example.test/stable-12.jar"},
			},
		},
		{
			ID:      15,
			Channel: "stable",
			Downloads: map[string]paperFillDownload{
				"server:default": {URL: "https://example.test/stable-15.jar"},
			},
		},
	}
	got, ok := selectPaperDownloadURL(builds)
	if !ok {
		t.Fatal("expected a Paper download URL")
	}
	if want := "https://example.test/stable-15.jar"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDownloadReplacesExistingDestinationAndStalePart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("fresh jar"))
	}))
	defer srv.Close()

	dst := filepath.Join(t.TempDir(), "BuildTools.jar")
	if err := os.WriteFile(dst, []byte("old jar"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst+".part", []byte("stale partial"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := download(context.Background(), srv.URL, dst); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "fresh jar" {
		t.Fatalf("downloaded content = %q, want fresh jar", string(got))
	}
	if _, err := os.Stat(dst + ".part"); !os.IsNotExist(err) {
		t.Fatalf("stale part file still exists or stat failed: %v", err)
	}
}
