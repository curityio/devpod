//go:build linux

package cmd

import (
	"slices"
	"testing"
)

func TestGetUserInfo(t *testing.T) {
	// root (uid 0, gid 0) is in /etc/passwd everywhere; 54321 is assumed to be in neither file.
	tests := []struct {
		spec     string
		homeDir  string
		uid, gid uint32
	}{
		{spec: "", homeDir: "/root", uid: 0, gid: 0},
		{spec: "root", homeDir: "/root", uid: 0, gid: 0},
		{spec: "0", homeDir: "/root", uid: 0, gid: 0},
		{spec: "0:0", homeDir: "/root", uid: 0, gid: 0},
		{spec: "root:54321", homeDir: "/root", uid: 0, gid: 54321},
		{spec: "54321", homeDir: "/", uid: 54321, gid: 0},
		{spec: "54321:54322", homeDir: "/", uid: 54321, gid: 54322},
		{spec: ":54322", homeDir: "/root", uid: 0, gid: 54322},
	}
	for _, test := range tests {
		t.Run(test.spec, func(t *testing.T) {
			homeDir, credential, err := getUserInfo(test.spec)
			if err != nil {
				t.Fatalf("getUserInfo(%q): %v", test.spec, err)
			}
			if homeDir != test.homeDir || credential.Uid != test.uid || credential.Gid != test.gid {
				t.Errorf("getUserInfo(%q) = %q, uid %d, gid %d; want %q, uid %d, gid %d",
					test.spec, homeDir, credential.Uid, credential.Gid, test.homeDir, test.uid, test.gid)
			}
		})
	}

	for _, spec := range []string{"no-such-user", "root:no-such-group"} {
		_, _, err := getUserInfo(spec)
		if err == nil {
			t.Errorf("getUserInfo(%q) succeeded, want an error", spec)
		}
	}
}

func TestGetContainerEnv(t *testing.T) {
	imageEnv := []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "FROM_IMAGE=1"}
	processEnv := []string{
		// the dockerless image's own environment
		"HOME=/root",
		"USER=root",
		"PATH=/usr/local/bin:/.dockerless/bin",
		"KANIKO_DIR=/.dockerless",
		"SSL_CERT_DIR=/.dockerless/ssl/certs",
		// what DevPod sets for dockerless
		"DOCKERLESS=true",
		"DOCKERLESS_CONTEXT=/workspaces/x",
		// containerEnv and DevPod's variables
		"FROM_IMAGE=2",
		"MY_VAR=value",
		"DEVPOD=true",
	}

	env := getContainerEnv("/home/dev", imageEnv, processEnv)

	want := []string{"HOME=/home/dev", "PATH=/usr/bin:/bin", "LANG=C.UTF-8", "FROM_IMAGE=1", "FROM_IMAGE=2", "MY_VAR=value", "DEVPOD=true"}
	if !slices.Equal(env, want) {
		t.Errorf("getContainerEnv() = %q, want %q", env, want)
	}

	// A variable the container was started with overrides the dockerless image's value.
	env = getContainerEnv("/home/dev", nil, []string{"PATH=/opt/bin"})
	if !slices.Contains(env, "PATH=/opt/bin") {
		t.Errorf("getContainerEnv() = %q, want it to keep PATH=/opt/bin", env)
	}
}
