package image

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/loft-sh/devpod/pkg/kubeclient"
	"gotest.tools/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

func TestServiceAccountKeychain(t *testing.T) {
	objects := map[string]any{
		"/api/v1/namespaces/ns/serviceaccounts/builder": corev1.ServiceAccount{
			TypeMeta:         metav1.TypeMeta{Kind: "ServiceAccount", APIVersion: "v1"},
			ObjectMeta:       metav1.ObjectMeta{Name: "builder", Namespace: "ns"},
			ImagePullSecrets: []corev1.LocalObjectReference{{Name: "missing"}, {Name: "acr"}},
		},
		"/api/v1/namespaces/ns/secrets/acr": corev1.Secret{
			TypeMeta:   metav1.TypeMeta{Kind: "Secret", APIVersion: "v1"},
			ObjectMeta: metav1.ObjectMeta{Name: "acr", Namespace: "ns"},
			Type:       corev1.SecretTypeDockerConfigJson,
			Data: map[string][]byte{
				corev1.DockerConfigJsonKey: []byte(`{"auths":{"myregistry.azurecr.io":{"username":"user","password":"pass"}}}`),
			},
		},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		obj, ok := objects[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			obj = metav1.Status{
				TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
				Status:   metav1.StatusFailure,
				Reason:   metav1.StatusReasonNotFound,
				Code:     http.StatusNotFound,
			}
		}
		_ = json.NewEncoder(w).Encode(obj)
	}))
	defer server.Close()

	client, err := kubeclient.NewForConfig(&rest.Config{Host: server.URL})
	assert.NilError(t, err)

	keychain, err := newServiceAccountKeychain(context.Background(), client, "ns", "builder")
	assert.NilError(t, err)

	ref, err := name.ParseReference("myregistry.azurecr.io/team/image:latest")
	assert.NilError(t, err)
	auth, err := keychain.Resolve(ref.Context())
	assert.NilError(t, err)
	cfg, err := auth.Authorization()
	assert.NilError(t, err)
	assert.Equal(t, cfg.Username, "user")
	assert.Equal(t, cfg.Password, "pass")

	ref, err = name.ParseReference("ghcr.io/other/image:latest")
	assert.NilError(t, err)
	auth, err = keychain.Resolve(ref.Context())
	assert.NilError(t, err)
	assert.Equal(t, auth, authn.Anonymous)

	// a missing service account gives an empty keychain, not an error
	keychain, err = newServiceAccountKeychain(context.Background(), client, "ns", "nobody")
	assert.NilError(t, err)
	auth, err = keychain.Resolve(ref.Context())
	assert.NilError(t, err)
	assert.Equal(t, auth, authn.Anonymous)
}

func TestKeychainFromPullSecretsMatching(t *testing.T) {
	keychain, err := newKeychainFromPullSecrets([]corev1.Secret{{
		Type: corev1.SecretTypeDockercfg,
		Data: map[string][]byte{
			corev1.DockerConfigKey: []byte(`{"https://*.example.com/v2/":{"username":"wild","password":"card"}}`),
		},
	}})
	assert.NilError(t, err)

	for image, wantAuth := range map[string]bool{
		"registry.example.com/image": true,
		"example.com/image":          false,
		"a.b.example.com/image":      false,
	} {
		ref, err := name.ParseReference(image)
		assert.NilError(t, err)
		auth, err := keychain.Resolve(ref.Context())
		assert.NilError(t, err)
		assert.Equal(t, auth != authn.Anonymous, wantAuth, image)
	}
}
