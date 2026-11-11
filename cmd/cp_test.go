package cmd

import (
	"testing"
)

func TestParseCpLocation(t *testing.T) {
	tests := []struct {
		arg       string
		goos      string
		workspace string
		path      string
		err       bool
	}{
		{arg: "my-file.txt", path: "my-file.txt"},
		{arg: "./here/file-here.txt", path: "./here/file-here.txt"},
		{arg: "foo:", workspace: "foo", path: ""},
		{arg: "foo:file-in-container.txt", workspace: "foo", path: "file-in-container.txt"},
		{arg: "foo:/usr/local/", workspace: "foo", path: "/usr/local/"},
		{arg: "foo:a:b", workspace: "foo", path: "a:b"},
		{arg: "./notes:2024.txt", path: "./notes:2024.txt"},
		{arg: "dir/notes:2024.txt", path: "dir/notes:2024.txt"},
		{arg: `dir\notes:2024.txt`, path: `dir\notes:2024.txt`},
		{arg: ":file", err: true},
		{arg: "c:file", goos: "linux", workspace: "c", path: "file"},
		{arg: `C:\Users\me\file.txt`, goos: "windows", path: `C:\Users\me\file.txt`},
		{arg: "C:file.txt", goos: "windows", path: "C:file.txt"},
		{arg: "foo:file.txt", goos: "windows", workspace: "foo", path: "file.txt"},
	}
	for _, tc := range tests {
		t.Run(tc.arg, func(t *testing.T) {
			goos := tc.goos
			if goos == "" {
				goos = "linux"
			}
			location, err := parseCpLocation(tc.arg, goos)
			if tc.err {
				if err == nil {
					t.Fatalf("expected an error, got %+v", location)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if location.workspace != tc.workspace || location.path != tc.path {
				t.Errorf("expected workspace %q and path %q, got %+v", tc.workspace, tc.path, location)
			}
		})
	}
}
