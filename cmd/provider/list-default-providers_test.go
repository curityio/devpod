package provider

import (
	"slices"
	"testing"
)

func TestAvailableProviders(t *testing.T) {
	got := availableProviders([]string{"devpod-provider-hetzner", "devpod-provider-aws", "devpod", "devpod-provider-"})
	want := []string{
		"aws",
		"azure",
		"civo",
		"digitalocean",
		"dockerless",
		"ecs",
		"gcloud",
		"hetzner",
		"kubernetes",
		"ssh",
		"terraform",
	}
	if !slices.Equal(got, want) {
		t.Errorf("availableProviders() = %v, want %v", got, want)
	}
}
