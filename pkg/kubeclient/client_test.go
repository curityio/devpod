package kubeclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"gotest.tools/assert"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

type request struct {
	method, path, query, body string
}

// fakeAPIServer records requests and answers them with handler.
func fakeAPIServer(t *testing.T, handler func(r request) (int, any)) (*Clientset, *[]request) {
	t.Helper()
	var requests []request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		req := request{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, body: string(body)}
		requests = append(requests, req)
		status, response := handler(req)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if s, ok := response.(string); ok {
			_, _ = w.Write([]byte(s))
			return
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	t.Cleanup(server.Close)

	client, err := NewForConfig(&rest.Config{Host: server.URL})
	assert.NilError(t, err)
	return client, &requests
}

func notFound(kind, name string) metav1.Status {
	return metav1.Status{
		TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
		Status:   metav1.StatusFailure,
		Reason:   metav1.StatusReasonNotFound,
		Code:     http.StatusNotFound,
		Message:  kind + ` "` + name + `" not found`,
		Details:  &metav1.StatusDetails{Name: name, Kind: kind},
	}
}

func TestGetPod(t *testing.T) {
	client, requests := fakeAPIServer(t, func(r request) (int, any) {
		return http.StatusOK, corev1.Pod{
			TypeMeta:   metav1.TypeMeta{Kind: "Pod", APIVersion: "v1"},
			ObjectMeta: metav1.ObjectMeta{Name: "ws", Namespace: "ns"},
			Status:     corev1.PodStatus{Phase: corev1.PodRunning},
		}
	})

	pod, err := client.CoreV1().Pods("ns").Get(context.Background(), "ws", metav1.GetOptions{})
	assert.NilError(t, err)
	assert.Equal(t, pod.Name, "ws")
	assert.Equal(t, pod.Status.Phase, corev1.PodRunning)
	assert.Equal(t, (*requests)[0].method, http.MethodGet)
	assert.Equal(t, (*requests)[0].path, "/api/v1/namespaces/ns/pods/ws")
}

func TestGetNotFound(t *testing.T) {
	client, _ := fakeAPIServer(t, func(r request) (int, any) {
		return http.StatusNotFound, notFound("persistentvolumeclaims", "ws")
	})

	_, err := client.CoreV1().PersistentVolumeClaims("ns").Get(context.Background(), "ws", metav1.GetOptions{})
	assert.Assert(t, kerrors.IsNotFound(err), "expected not found error, got %v", err)
}

func TestCreateAlreadyExists(t *testing.T) {
	client, requests := fakeAPIServer(t, func(r request) (int, any) {
		return http.StatusConflict, metav1.Status{
			TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
			Status:   metav1.StatusFailure,
			Reason:   metav1.StatusReasonAlreadyExists,
			Code:     http.StatusConflict,
		}
	})

	_, err := client.RbacV1().RoleBindings("ns").Create(context.Background(), &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "ws"},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "edit"},
	}, metav1.CreateOptions{})
	assert.Assert(t, kerrors.IsAlreadyExists(err), "expected already exists error, got %v", err)

	req := (*requests)[0]
	assert.Equal(t, req.method, http.MethodPost)
	assert.Equal(t, req.path, "/apis/rbac.authorization.k8s.io/v1/namespaces/ns/rolebindings")
	var sent map[string]any
	assert.NilError(t, json.Unmarshal([]byte(req.body), &sent))
	assert.Equal(t, sent["kind"], "RoleBinding")
	assert.Equal(t, sent["apiVersion"], "rbac.authorization.k8s.io/v1")
}

func TestCreateClusterScoped(t *testing.T) {
	client, requests := fakeAPIServer(t, func(r request) (int, any) {
		return http.StatusCreated, r.body
	})

	ns, err := client.CoreV1().Namespaces().Create(context.Background(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "devpod"},
	}, metav1.CreateOptions{})
	assert.NilError(t, err)
	assert.Equal(t, ns.Name, "devpod")
	assert.Equal(t, (*requests)[0].path, "/api/v1/namespaces")
}

func TestDelete(t *testing.T) {
	client, requests := fakeAPIServer(t, func(r request) (int, any) {
		return http.StatusOK, metav1.Status{TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"}, Status: metav1.StatusSuccess}
	})

	err := client.CoreV1().Pods("ns").Delete(context.Background(), "ws", metav1.DeleteOptions{GracePeriodSeconds: &[]int64{10}[0]})
	assert.NilError(t, err)

	req := (*requests)[0]
	assert.Equal(t, req.method, http.MethodDelete)
	assert.Equal(t, req.path, "/api/v1/namespaces/ns/pods/ws")
	var sent metav1.DeleteOptions
	assert.NilError(t, json.Unmarshal([]byte(req.body), &sent))
	assert.Equal(t, *sent.GracePeriodSeconds, int64(10))
}

func TestListNodes(t *testing.T) {
	client, requests := fakeAPIServer(t, func(r request) (int, any) {
		return http.StatusOK, corev1.NodeList{
			TypeMeta: metav1.TypeMeta{Kind: "NodeList", APIVersion: "v1"},
			Items: []corev1.Node{
				{Status: corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{Architecture: "arm64"}}},
			},
		}
	})

	nodes, err := client.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
	assert.NilError(t, err)
	assert.Equal(t, len(nodes.Items), 1)
	assert.Equal(t, nodes.Items[0].Status.NodeInfo.Architecture, "arm64")
	assert.Equal(t, (*requests)[0].path, "/api/v1/nodes")
}

func TestGetLogs(t *testing.T) {
	client, requests := fakeAPIServer(t, func(r request) (int, any) {
		return http.StatusOK, "hello"
	})

	stream, err := client.CoreV1().Pods("ns").GetLogs("ws", &corev1.PodLogOptions{Container: "devpod", Follow: true}).Stream(context.Background())
	assert.NilError(t, err)
	defer stream.Close()
	logs, err := io.ReadAll(stream)
	assert.NilError(t, err)
	assert.Equal(t, string(logs), "hello")

	req := (*requests)[0]
	assert.Equal(t, req.path, "/api/v1/namespaces/ns/pods/ws/log")
	assert.Equal(t, req.query, "container=devpod&follow=true")
}

func TestExecURL(t *testing.T) {
	client, _ := fakeAPIServer(t, func(r request) (int, any) { return http.StatusOK, "" })

	url := client.CoreV1().RESTClient().Post().
		Resource("pods").
		Name("ws").
		Namespace("ns").
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: "devpod",
			Command:   []string{"sh", "-c", "true"},
			Stdin:     true,
			Stdout:    true,
		}, ParameterCodec).
		URL()

	assert.Equal(t, url.Path, "/api/v1/namespaces/ns/pods/ws/exec")
	assert.Equal(t, url.RawQuery, "command=sh&command=-c&command=true&container=devpod&stdin=true&stdout=true")
}
