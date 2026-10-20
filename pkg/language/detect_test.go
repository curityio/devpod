package language

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectLanguage(t *testing.T) {
	tests := []struct {
		name  string
		files []string
		want  ProgrammingLanguage
	}{
		{name: "empty", files: nil, want: None},
		{name: "no sources", files: []string{"README.md", "config.yaml"}, want: None},
		{name: "go", files: []string{"main.go", "pkg/env.go", "pkg/x/y.go", "script.py"}, want: Go},
		{name: "typescript maps to javascript", files: []string{"src/a.ts", "src/b.ts", "src/types.d.ts"}, want: JavaScript},
		{name: "c maps to c++", files: []string{"main.c"}, want: Cpp},
		{name: "c++", files: []string{"main.cpp"}, want: Cpp},
		{name: "c#", files: []string{"Program.cs"}, want: DotNet},
		{name: "php", files: []string{"index.php"}, want: PHP},
		{
			name:  "skips dependencies, hidden and generated files",
			files: []string{"app.py", "node_modules/a/a.js", "node_modules/b/b.js", "vendor/x.go", "Vendor/y.go", ".hidden/z.go", "dist/app.min.js", "lib.min.js"},
			want:  Python,
		},
		{name: "ties use a fixed order", files: []string{"a.rb", "b.java"}, want: Java},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, f := range tt.files {
				path := filepath.Join(dir, f)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := DetectLanguage(dir)
			if err != nil {
				t.Fatalf("DetectLanguage() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("DetectLanguage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDetectStopsAtFileLimit(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"a.go", "b.go", "c.py", "d.py", "e.py"} {
		if err := os.WriteFile(filepath.Join(dir, f), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// WalkDir visits entries in lexical order, so only a.go and b.go are counted
	got, err := detect(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got != Go {
		t.Errorf("detect() = %q, want %q", got, Go)
	}
}

func TestDetectLanguageFollowsSymlinkedRoot(t *testing.T) {
	dir := t.TempDir()
	project := filepath.Join(dir, "project")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "main.go"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(project, link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	got, err := DetectLanguage(link)
	if err != nil {
		t.Fatal(err)
	}
	if got != Go {
		t.Errorf("DetectLanguage() = %q, want %q", got, Go)
	}
}
