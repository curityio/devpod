package workspace

import "testing"

func TestGithubProviderRepo(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{name: "aws", want: "loft-sh/devpod-provider-aws"},
		{name: "ssh", want: "loft-sh/devpod-provider-ssh"},
		{name: "terraform", want: "loft-sh/devpod-provider-terraform"},
		{name: "hetzner", want: "curityio/devpod-provider-hetzner"},
		{name: "aws2", want: "curityio/devpod-provider-aws2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GithubProviderRepo(tt.name); got != tt.want {
				t.Errorf("GithubProviderRepo(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}
