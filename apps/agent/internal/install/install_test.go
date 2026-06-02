package install

import "testing"

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
