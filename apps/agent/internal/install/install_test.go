package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func paperBuild(id int, channel, url, sha string) paperFillBuild {
	d := paperFillDownload{URL: url}
	d.Checksums.SHA256 = sha
	return paperFillBuild{
		ID:        id,
		Channel:   channel,
		Downloads: map[string]paperFillDownload{"server:default": d},
	}
}

func TestSelectPaperDownloadURL(t *testing.T) {
	builds := []paperFillBuild{
		paperBuild(12, "stable", "https://example.test/stable-12.jar", "aaa"),
		paperBuild(14, "beta", "https://example.test/beta-14.jar", "bbb"),
		paperBuild(9, "recommended", "https://example.test/recommended-9.jar", "ccc"),
	}
	got, sha, ok := selectPaperDownloadURL(builds)
	if !ok {
		t.Fatal("expected a Paper download URL")
	}
	if want := "https://example.test/recommended-9.jar"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if sha != "ccc" {
		t.Fatalf("sha = %q, want the winning build's checksum %q", sha, "ccc")
	}
}

func TestSelectPaperDownloadURLUsesNewestWithinChannel(t *testing.T) {
	builds := []paperFillBuild{
		paperBuild(12, "stable", "https://example.test/stable-12.jar", "aaa"),
		paperBuild(15, "stable", "https://example.test/stable-15.jar", "bbb"),
	}
	got, sha, ok := selectPaperDownloadURL(builds)
	if !ok {
		t.Fatal("expected a Paper download URL")
	}
	if want := "https://example.test/stable-15.jar"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if sha != "bbb" {
		t.Fatalf("sha = %q, want %q", sha, "bbb")
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

	if err := download(context.Background(), srv.URL, dst, integrity{}); err != nil {
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

func TestDownloadVerifiesChecksum(t *testing.T) {
	payload := []byte("server jar payload")
	sum := sha256.Sum256(payload)
	good := hex.EncodeToString(sum[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	t.Run("matching digest passes", func(t *testing.T) {
		dst := filepath.Join(t.TempDir(), "server.jar")
		want := integrity{algo: "sha256", hex: good, size: int64(len(payload))}
		if err := download(context.Background(), srv.URL, dst, want); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(dst)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(payload) {
			t.Fatalf("content = %q", got)
		}
	})

	t.Run("uppercase digest still matches", func(t *testing.T) {
		dst := filepath.Join(t.TempDir(), "server.jar")
		want := integrity{algo: "sha256", hex: strings.ToUpper(good)}
		if err := download(context.Background(), srv.URL, dst, want); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("wrong digest fails and cleans up", func(t *testing.T) {
		dst := filepath.Join(t.TempDir(), "server.jar")
		bad := strings.Repeat("0", 64)
		err := download(context.Background(), srv.URL, dst, integrity{algo: "sha256", hex: bad})
		if err == nil {
			t.Fatal("expected checksum mismatch error")
		}
		if !strings.Contains(err.Error(), good) || !strings.Contains(err.Error(), bad) {
			t.Fatalf("error should name both digests, got: %v", err)
		}
		if _, statErr := os.Stat(dst); !os.IsNotExist(statErr) {
			t.Fatalf("destination should not exist after mismatch: %v", statErr)
		}
		if _, statErr := os.Stat(dst + ".part"); !os.IsNotExist(statErr) {
			t.Fatalf(".part should be removed after mismatch: %v", statErr)
		}
	})

	t.Run("size mismatch fails", func(t *testing.T) {
		dst := filepath.Join(t.TempDir(), "server.jar")
		want := integrity{algo: "sha256", hex: good, size: int64(len(payload)) + 1}
		err := download(context.Background(), srv.URL, dst, want)
		if err == nil || !strings.Contains(err.Error(), "size mismatch") {
			t.Fatalf("expected size mismatch error, got: %v", err)
		}
		if _, statErr := os.Stat(dst + ".part"); !os.IsNotExist(statErr) {
			t.Fatalf(".part should be removed after mismatch: %v", statErr)
		}
	})

	t.Run("unknown algorithm fails", func(t *testing.T) {
		dst := filepath.Join(t.TempDir(), "server.jar")
		err := download(context.Background(), srv.URL, dst, integrity{algo: "crc32", hex: "abcd"})
		if err == nil || !strings.Contains(err.Error(), "unsupported checksum algorithm") {
			t.Fatalf("expected unsupported-algorithm error, got: %v", err)
		}
	})
}

func TestMavenChecksum(t *testing.T) {
	sha256Hex := strings.Repeat("ab", 32) // 64 chars
	sha1Hex := strings.Repeat("cd", 20)   // 40 chars

	mux := http.NewServeMux()
	mux.HandleFunc("/both.jar.sha256", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sha256Hex + "\n"))
	})
	mux.HandleFunc("/both.jar.sha1", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sha1Hex))
	})
	// Maven-style "digest  filename" body, sha1 only.
	mux.HandleFunc("/sha1only.jar.sha1", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sha1Hex + "  sha1only.jar\n"))
	})
	// Sidecar exists but is garbage (wrong length / not hex).
	mux.HandleFunc("/garbage.jar.sha256", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html>not a checksum</html>"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx := context.Background()

	t.Run("prefers sha256", func(t *testing.T) {
		got := mavenChecksum(ctx, srv.URL+"/both.jar")
		if got.algo != "sha256" || got.hex != sha256Hex {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("falls back to sha1", func(t *testing.T) {
		got := mavenChecksum(ctx, srv.URL+"/sha1only.jar")
		if got.algo != "sha1" || got.hex != sha1Hex {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("no sidecar yields no verification", func(t *testing.T) {
		got := mavenChecksum(ctx, srv.URL+"/missing.jar")
		if got.enabled() {
			t.Fatalf("expected disabled integrity, got %+v", got)
		}
	})

	t.Run("garbage sidecar yields no verification", func(t *testing.T) {
		got := mavenChecksum(ctx, srv.URL+"/garbage.jar")
		if got.enabled() {
			t.Fatalf("expected disabled integrity, got %+v", got)
		}
	})
}
