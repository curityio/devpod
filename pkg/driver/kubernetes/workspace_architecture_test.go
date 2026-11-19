package kubernetes

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/curityio/devpod/pkg/devcontainer/config"
	"github.com/curityio/devpod/pkg/driver"
	"github.com/curityio/devpod/pkg/kubeclient"
	"github.com/curityio/devpod/pkg/log"
	provider2 "github.com/curityio/devpod/pkg/provider"
	"gotest.tools/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/rest"
)

const (
	pvcsPath = "/api/v1/namespaces/" + testNamespace + "/persistentvolumeclaims"
	podsPath = "/api/v1/namespaces/" + testNamespace + "/pods"
	podPath  = podsPath + "/devpod-ws"
)

// fakeCluster is an API server that keeps the persistent volume claim and the pod of workspace "ws", and runs a pod
// as soon as it's created, on the first node its node selector matches.
type fakeCluster struct {
	mu sync.Mutex

	nodes          []corev1.Node
	nodesForbidden bool
	// scaleUp is added to the nodes when a pod is created that no node can run, as a cluster autoscaler does
	scaleUp *corev1.Node

	pvc *corev1.PersistentVolumeClaim
	pod *corev1.Pod

	podsCreated int
	pvcPatches  int
}

func (c *fakeCluster) driver(t *testing.T) *KubernetesDriver {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		status, response := c.handle(r, body)
		c.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(response)
	}))
	t.Cleanup(server.Close)

	client, err := kubeclient.NewForConfig(&rest.Config{Host: server.URL})
	assert.NilError(t, err)
	return &KubernetesDriver{
		namespace: testNamespace,
		client:    &Client{client: client},
		options:   &provider2.ProviderKubernetesDriverConfig{DiskSize: "10Gi", PodTimeout: "10s"},
		Log:       log.Discard,
	}
}

func (c *fakeCluster) handle(r *http.Request, body []byte) (int, any) {
	switch r.Method + " " + r.URL.Path {
	case "GET " + nodesPath:
		if c.nodesForbidden {
			return forbidden("nodes", "")
		}
		list := corev1.NodeList{}
		name, byName := strings.CutPrefix(r.URL.Query().Get("fieldSelector"), "metadata.name=")
		for _, node := range c.nodes {
			if !byName || node.Name == name {
				list.Items = append(list.Items, node)
			}
		}
		return http.StatusOK, list
	case "GET " + pvcPath:
		if c.pvc == nil {
			return notFound("persistentvolumeclaims", "devpod-ws")
		}
		return http.StatusOK, c.pvc
	case "POST " + pvcsPath:
		c.pvc = &corev1.PersistentVolumeClaim{}
		if err := json.Unmarshal(body, c.pvc); err != nil {
			return http.StatusBadRequest, nil
		}
		return http.StatusCreated, c.pvc
	case "PATCH " + pvcPath:
		if c.pvc == nil {
			return notFound("persistentvolumeclaims", "devpod-ws")
		}
		// a JSON merge patch of annotations
		patch := &corev1.PersistentVolumeClaim{}
		if r.Header.Get("Content-Type") != "application/merge-patch+json" || json.Unmarshal(body, patch) != nil {
			return http.StatusBadRequest, nil
		}
		for key, value := range patch.Annotations {
			c.pvc.Annotations[key] = value
		}
		c.pvcPatches++
		return http.StatusOK, c.pvc
	case "GET " + podPath:
		if c.pod == nil {
			return notFound("pods", "devpod-ws")
		}
		return http.StatusOK, c.pod
	case "POST " + podsPath:
		c.pod = &corev1.Pod{}
		if err := json.Unmarshal(body, c.pod); err != nil {
			return http.StatusBadRequest, nil
		}
		c.podsCreated++
		c.schedule()
		return http.StatusCreated, c.pod
	case "DELETE " + podPath:
		c.pod = nil
		return http.StatusOK, metav1.Status{Status: metav1.StatusSuccess}
	}

	return http.StatusNotFound, nil
}

// schedule runs the pod on the first untainted node its node selector matches, and leaves it pending if there is none.
func (c *fakeCluster) schedule() {
	c.pod.Status.Phase = corev1.PodPending
	node := c.nodeFor(c.pod)
	if node == nil && c.scaleUp != nil {
		c.nodes = append(c.nodes, *c.scaleUp)
		node = c.nodeFor(c.pod)
	}
	if node == nil {
		return
	}

	c.pod.Spec.NodeName = node.Name
	c.pod.Status.Phase = corev1.PodRunning
	for _, container := range c.pod.Spec.Containers {
		c.pod.Status.ContainerStatuses = append(c.pod.Status.ContainerStatuses, corev1.ContainerStatus{
			Name:  container.Name,
			Ready: true,
			State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
		})
	}
}

func (c *fakeCluster) nodeFor(pod *corev1.Pod) *corev1.Node {
	for i := range c.nodes {
		node := &c.nodes[i]
		if len(node.Spec.Taints) == 0 && labels.SelectorFromSet(pod.Spec.NodeSelector).Matches(labels.Set(node.Labels)) {
			return node
		}
	}

	return nil
}

// persisted returns the run options kept on the workspace's persistent volume claim.
func (c *fakeCluster) persisted(t *testing.T) *driver.RunOptions {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	info := &DevContainerInfo{}
	assert.NilError(t, json.Unmarshal([]byte(c.pvc.Annotations[DevPodInfoAnnotation]), info))
	return info.Options
}

// running returns the image, the kubernetes.io/arch node selector and the node of the workspace's pod.
func (c *fakeCluster) running(t *testing.T) (image, archSelector, node string) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	assert.Assert(t, c.pod != nil, "no pod")
	return c.pod.Spec.Containers[0].Image, c.pod.Spec.NodeSelector[corev1.LabelArchStable], c.pod.Spec.NodeName
}

// requests returns how many pods were created and how often the persistent volume claim was patched.
func (c *fakeCluster) requests() (podsCreated, pvcPatches int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.podsCreated, c.pvcPatches
}

func (c *fakeCluster) set(change func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	change()
}

func runOptions(image, arch string) *driver.RunOptions {
	return &driver.RunOptions{
		UID:            "uid",
		Image:          image,
		Architecture:   arch,
		Entrypoint:     "/bin/sh",
		WorkspaceMount: &config.Mount{Type: "bind", Source: "/src", Target: "/workspaces/ws"},
	}
}

// restart stops the workspace and starts it again, as `devpod stop` and `devpod up` do.
func restart(t *testing.T, k *KubernetesDriver) {
	t.Helper()
	ctx := context.Background()
	assert.NilError(t, k.StopDevContainer(ctx, "ws"))
	assert.NilError(t, k.StartDevContainer(ctx, "ws"))
	assert.NilError(t, k.RunDevContainer(ctx, "ws", nil))
}

// `devpod up --recreate` keeps the persistent volume claim, so the new image and its architecture must replace the
// ones kept on it, or the next start runs the old image again.
func TestRecreateReplacesRunOptions(t *testing.T) {
	ctx := context.Background()
	cluster := &fakeCluster{nodes: []corev1.Node{testNode("arm-0", "arm64"), testNode("workspace-0", "amd64")}}
	k := cluster.driver(t)

	assert.NilError(t, k.RunDevContainer(ctx, "ws", runOptions("registry.example.com/ws:amd64", "amd64")))
	image, arch, node := cluster.running(t)
	assert.Equal(t, image, "registry.example.com/ws:amd64")
	assert.Equal(t, arch, "amd64")
	assert.Equal(t, node, "workspace-0")

	// recreate: stop, then run with the options of the new build
	assert.NilError(t, k.StopDevContainer(ctx, "ws"))
	assert.NilError(t, k.RunDevContainer(ctx, "ws", runOptions("registry.example.com/ws:arm64", "arm64")))
	image, arch, node = cluster.running(t)
	assert.Equal(t, image, "registry.example.com/ws:arm64")
	assert.Equal(t, arch, "arm64")
	assert.Equal(t, node, "arm-0")
	assert.Equal(t, cluster.persisted(t).Image, "registry.example.com/ws:arm64")
	assert.Equal(t, cluster.persisted(t).Architecture, "arm64")

	restart(t, k)
	image, arch, node = cluster.running(t)
	assert.Equal(t, image, "registry.example.com/ws:arm64")
	assert.Equal(t, arch, "arm64")
	assert.Equal(t, node, "arm-0")
}

func TestRecreateWithSameRunOptions(t *testing.T) {
	ctx := context.Background()
	cluster := &fakeCluster{nodes: []corev1.Node{testNode("workspace-0", "amd64")}}
	k := cluster.driver(t)

	assert.NilError(t, k.RunDevContainer(ctx, "ws", runOptions("registry.example.com/ws:amd64", "amd64")))
	assert.NilError(t, k.StopDevContainer(ctx, "ws"))
	assert.NilError(t, k.RunDevContainer(ctx, "ws", runOptions("registry.example.com/ws:amd64", "amd64")))
	podsCreated, pvcPatches := cluster.requests()
	assert.Equal(t, pvcPatches, 0)
	assert.Equal(t, podsCreated, 2)
}

func TestRecreateWithoutPermissionToPatch(t *testing.T) {
	ctx := context.Background()
	k, _ := fakeDriver(t, func(method, path string) (int, any) {
		switch method + " " + path {
		case "GET " + pvcPath:
			pvc, err := (&KubernetesDriver{options: &provider2.ProviderKubernetesDriverConfig{DiskSize: "10Gi"}, Log: log.Discard}).
				buildPersistentVolumeClaim("devpod-ws", runOptions("registry.example.com/ws:amd64", "amd64"))
			if err != nil {
				return http.StatusInternalServerError, nil
			}
			return http.StatusOK, pvc
		case "PATCH " + pvcPath:
			return forbidden("persistentvolumeclaims", "devpod-ws")
		}
		return http.StatusNotFound, nil
	})

	// it must not run the new image while the old one stays with the workspace
	err := k.RunDevContainer(ctx, "ws", runOptions("registry.example.com/ws:arm64", "arm64"))
	assert.ErrorContains(t, err, "DevPod needs the permission to patch persistent volume claims in namespace 'dev-ns'")
}

// A workspace whose architecture wasn't known when it was built (here: its node pool was scaled to zero) keeps the
// architecture of the node it first runs on, whatever the nodes are when it restarts.
func TestKeepsArchitectureOfFirstNode(t *testing.T) {
	ctx := context.Background()
	system := testNode("system-0", "amd64", tainted(criticalAddonsOnly))
	scaleUp := testNode("workspace-0", "arm64")
	cluster := &fakeCluster{nodes: []corev1.Node{system}, scaleUp: &scaleUp}
	k := cluster.driver(t)

	arch, err := k.TargetArchitecture(ctx, "ws")
	assert.NilError(t, err)
	assert.Equal(t, arch, "")

	assert.NilError(t, k.RunDevContainer(ctx, "ws", runOptions("ghcr.io/curityio/dockerless:test", arch)))
	_, selector, node := cluster.running(t)
	assert.Equal(t, selector, "")
	assert.Equal(t, node, "workspace-0")
	assert.Equal(t, cluster.persisted(t).Architecture, "arm64")
	assert.Equal(t, cluster.persisted(t).Image, "ghcr.io/curityio/dockerless:test")

	// an amd64 node the pod can run on too is listed before the arm64 one
	cluster.set(func() {
		cluster.nodes = []corev1.Node{system, testNode("workspace-1", "amd64"), scaleUp}
	})
	restart(t, k)
	_, selector, node = cluster.running(t)
	assert.Equal(t, selector, "arm64")
	assert.Equal(t, node, "workspace-0")
}

// Without permission to list nodes, the architecture the pod was pinned to with the ARCHITECTURE option is kept,
// also when the option changes later.
func TestKeepsArchitectureOfOption(t *testing.T) {
	ctx := context.Background()
	cluster := &fakeCluster{
		nodes:          []corev1.Node{testNode("workspace-0", "amd64"), testNode("arm-0", "arm64")},
		nodesForbidden: true,
	}
	k := cluster.driver(t)
	k.options.Architecture = "arm64"

	// an image that wasn't built, so its architecture isn't known
	assert.NilError(t, k.RunDevContainer(ctx, "ws", runOptions("ubuntu:24.04", "")))
	_, selector, node := cluster.running(t)
	assert.Equal(t, selector, "arm64")
	assert.Equal(t, node, "arm-0")
	assert.Equal(t, cluster.persisted(t).Architecture, "arm64")

	k.options.Architecture = "amd64"
	restart(t, k)
	_, selector, node = cluster.running(t)
	assert.Equal(t, selector, "arm64")
	assert.Equal(t, node, "arm-0")
}

func TestArchitectureUnknownWithoutPermissionToListNodes(t *testing.T) {
	ctx := context.Background()
	cluster := &fakeCluster{nodes: []corev1.Node{testNode("workspace-0", "amd64")}, nodesForbidden: true}
	k := cluster.driver(t)

	assert.NilError(t, k.RunDevContainer(ctx, "ws", runOptions("ubuntu:24.04", "")))
	_, selector, node := cluster.running(t)
	assert.Equal(t, selector, "")
	assert.Equal(t, node, "workspace-0")
	assert.Equal(t, cluster.persisted(t).Architecture, "")
	_, pvcPatches := cluster.requests()
	assert.Equal(t, pvcPatches, 0)

	// once the nodes can be listed, `devpod up` of the running workspace records it, without replacing the pod
	cluster.set(func() { cluster.nodesForbidden = false })
	assert.NilError(t, k.RunDevContainer(ctx, "ws", nil))
	assert.Equal(t, cluster.persisted(t).Architecture, "amd64")
	podsCreated, _ := cluster.requests()
	assert.Equal(t, podsCreated, 1)
}

// A workspace created by an older DevPod has no architecture. When its running pod is replaced, here because a
// provider option changed, the new pod must run on the architecture of the old one.
func TestKeepsArchitectureOfReplacedPod(t *testing.T) {
	ctx := context.Background()
	cluster := &fakeCluster{
		nodes:          []corev1.Node{testNode("arm-0", "arm64"), testNode("workspace-0", "amd64")},
		nodesForbidden: true,
	}
	k := cluster.driver(t)

	// as the nodes can't be listed, the workspace's architecture stays unknown, like an older DevPod left it
	assert.NilError(t, k.RunDevContainer(ctx, "ws", runOptions("ghcr.io/curityio/dockerless:test", "")))
	_, selector, node := cluster.running(t)
	assert.Equal(t, selector, "")
	assert.Equal(t, node, "arm-0")
	assert.Equal(t, cluster.persisted(t).Architecture, "")

	cluster.set(func() { cluster.nodesForbidden = false })
	k.options.Architecture = "amd64"
	k.options.Labels = "changed=true"
	assert.NilError(t, k.RunDevContainer(ctx, "ws", nil))
	podsCreated, _ := cluster.requests()
	assert.Equal(t, podsCreated, 2)
	_, selector, node = cluster.running(t)
	assert.Equal(t, selector, "arm64")
	assert.Equal(t, node, "arm-0")
	assert.Equal(t, cluster.persisted(t).Architecture, "arm64")
}

// The node selector of a pod that isn't running says where it should run, not where it does.
func TestArchitectureOfPendingPodIsNotKept(t *testing.T) {
	ctx := context.Background()
	cluster := &fakeCluster{nodes: []corev1.Node{testNode("workspace-0", "amd64")}}
	k := cluster.driver(t)
	// an architecture no node has
	k.options.Architecture = "arm"
	k.options.PodTimeout = "1s"

	err := k.RunDevContainer(ctx, "ws", runOptions("ubuntu:24.04", ""))
	assert.Assert(t, err != nil, "expected the pod not to start")

	// `devpod up` again finds the pending pod unchanged
	assert.NilError(t, k.RunDevContainer(ctx, "ws", nil))
	assert.Equal(t, cluster.persisted(t).Architecture, "")
	_, pvcPatches := cluster.requests()
	assert.Equal(t, pvcPatches, 0)

	// so correcting the option is enough for the workspace to start
	k.options.Architecture = "amd64"
	assert.NilError(t, k.RunDevContainer(ctx, "ws", nil))
	_, selector, node := cluster.running(t)
	assert.Equal(t, selector, "amd64")
	assert.Equal(t, node, "workspace-0")
	assert.Equal(t, cluster.persisted(t).Architecture, "amd64")
}
