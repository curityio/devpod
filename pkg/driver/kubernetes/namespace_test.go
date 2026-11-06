package kubernetes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/loft-sh/devpod/pkg/kubeclient"
	"github.com/loft-sh/devpod/pkg/log"
	provider2 "github.com/loft-sh/devpod/pkg/provider"
	"gotest.tools/assert"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

const (
	testNamespace = "dev-ns"
	pvcPath       = "/api/v1/namespaces/" + testNamespace + "/persistentvolumeclaims/devpod-ws"
	namespacePath = "/api/v1/namespaces/" + testNamespace
)

// fakeDriver returns a driver whose API server answers each request with handler, and the requests it got as
// "METHOD path".
func fakeDriver(t *testing.T, handler func(method, path string) (int, any)) (*KubernetesDriver, func() []string) {
	t.Helper()
	var (
		mu       sync.Mutex
		requests []string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()
		status, body := handler(r.Method, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(server.Close)

	client, err := kubeclient.NewForConfig(&rest.Config{Host: server.URL})
	assert.NilError(t, err)
	return &KubernetesDriver{
			namespace: testNamespace,
			client:    &Client{client: client},
			options:   &provider2.ProviderKubernetesDriverConfig{},
			Log:       log.Discard,
		}, func() []string {
			mu.Lock()
			defer mu.Unlock()
			return append([]string(nil), requests...)
		}
}

func status(err *kerrors.StatusError) (int, any) {
	return int(err.ErrStatus.Code), err.ErrStatus
}

func forbidden(resource, name string) (int, any) {
	return status(kerrors.NewForbidden(corev1.Resource(resource), name, nil))
}

func notFound(resource, name string) (int, any) {
	return status(kerrors.NewNotFound(corev1.Resource(resource), name))
}

func namespace() corev1.Namespace {
	return corev1.Namespace{
		TypeMeta:   metav1.TypeMeta{Kind: "Namespace", APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{Name: testNamespace},
	}
}

func TestFindDevContainerInMissingNamespace(t *testing.T) {
	// Access to the namespace is only granted once it exists, so the PVC lookup is forbidden.
	k, requests := fakeDriver(t, func(method, path string) (int, any) {
		if path == pvcPath {
			return forbidden("persistentvolumeclaims", "devpod-ws")
		}
		return notFound("namespaces", testNamespace)
	})

	details, err := k.FindDevContainer(context.Background(), "ws")
	assert.NilError(t, err)
	assert.Assert(t, details == nil)
	assert.DeepEqual(t, requests(), []string{"GET " + pvcPath, "GET " + namespacePath})
}

func TestFindDevContainerForbiddenInExistingNamespace(t *testing.T) {
	k, _ := fakeDriver(t, func(method, path string) (int, any) {
		if path == pvcPath {
			return forbidden("persistentvolumeclaims", "devpod-ws")
		}
		return http.StatusOK, namespace()
	})

	_, err := k.FindDevContainer(context.Background(), "ws")
	assert.Assert(t, kerrors.IsForbidden(err), "got %v", err)
}

func TestFindDevContainerForbiddenNamespaceLookup(t *testing.T) {
	// Without permission to get the namespace, the PVC lookup's error stands.
	k, _ := fakeDriver(t, func(method, path string) (int, any) {
		if path == pvcPath {
			return forbidden("persistentvolumeclaims", "devpod-ws")
		}
		return forbidden("namespaces", testNamespace)
	})

	_, err := k.FindDevContainer(context.Background(), "ws")
	assert.Assert(t, kerrors.IsForbidden(err), "got %v", err)
}

func TestCreateNamespaceAndWaitForAccess(t *testing.T) {
	defer func(interval, timeout time.Duration) {
		namespaceAccessInterval, namespaceAccessTimeout = interval, timeout
	}(namespaceAccessInterval, namespaceAccessTimeout)
	namespaceAccessInterval, namespaceAccessTimeout = time.Millisecond, 10*time.Second

	pvcLookups := 0
	k, requests := fakeDriver(t, func(method, path string) (int, any) {
		switch {
		case method == http.MethodGet && path == namespacePath:
			return notFound("namespaces", testNamespace)
		case method == http.MethodPost && path == "/api/v1/namespaces":
			return http.StatusCreated, namespace()
		case path == pvcPath:
			// access is granted a little after the namespace is created
			pvcLookups++
			if pvcLookups < 3 {
				return forbidden("persistentvolumeclaims", "devpod-ws")
			}
			return notFound("persistentvolumeclaims", "devpod-ws")
		}
		t.Fatalf("unexpected request %s %s", method, path)
		return 0, nil
	})

	created, err := k.createNamespace(context.Background())
	assert.NilError(t, err)
	assert.Assert(t, created)

	k.waitForNamespaceAccess(context.Background(), "devpod-ws")
	assert.DeepEqual(t, requests(), []string{
		"GET " + namespacePath,
		"POST /api/v1/namespaces",
		"GET " + pvcPath,
		"GET " + pvcPath,
		"GET " + pvcPath,
	})
}

func TestCreateNamespaceThatExists(t *testing.T) {
	k, requests := fakeDriver(t, func(method, path string) (int, any) {
		return http.StatusOK, namespace()
	})

	created, err := k.createNamespace(context.Background())
	assert.NilError(t, err)
	assert.Assert(t, !created)
	assert.DeepEqual(t, requests(), []string{"GET " + namespacePath})
}

func TestCreateNamespaceForbidden(t *testing.T) {
	// The namespace may exist although the user can't get it, so a forbidden create isn't an error.
	k, _ := fakeDriver(t, func(method, path string) (int, any) {
		return forbidden("namespaces", testNamespace)
	})

	created, err := k.createNamespace(context.Background())
	assert.NilError(t, err)
	assert.Assert(t, !created)
}
