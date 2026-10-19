// Package kubeclient is a minimal Kubernetes client for the API groups DevPod uses (core/v1 and
// rbac.authorization.k8s.io/v1).
//
// It deliberately does not use k8s.io/client-go/kubernetes: that package (and even its per-group typed
// clients) initializes k8s.io/client-go/kubernetes/scheme, which links every Kubernetes API group into the
// binary and adds more than 20 MB to it. The methods below mirror the client-go typed clients so that callers
// read the same, and errors are the usual *errors.StatusError values that k8s.io/apimachinery/pkg/api/errors
// helpers such as IsNotFound understand.
package kubeclient

import (
	"context"
	"fmt"
	"net/http"
	"time"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"
)

var (
	// Scheme contains only the API types this client works with.
	Scheme = runtime.NewScheme()
	// Codecs provides serializers for Scheme.
	Codecs = serializer.NewCodecFactory(Scheme)
	// ParameterCodec converts option structs such as metav1.GetOptions or corev1.PodExecOptions to query parameters.
	ParameterCodec = runtime.NewParameterCodec(Scheme)
)

func init() {
	// same as k8s.io/client-go/kubernetes/scheme, restricted to the groups we use
	metav1.AddToGroupVersion(Scheme, schema.GroupVersion{Version: "v1"})
	utilruntime.Must(corev1.AddToScheme(Scheme))
	utilruntime.Must(rbacv1.AddToScheme(Scheme))
}

// Clientset gives access to the core/v1 and rbac.authorization.k8s.io/v1 APIs.
type Clientset struct {
	coreV1 *CoreV1Client
	rbacV1 *RbacV1Client
}

// NewForConfig creates a Clientset for the given config, sharing one HTTP client between the API groups.
func NewForConfig(c *rest.Config) (*Clientset, error) {
	config := *c
	if config.UserAgent == "" {
		config.UserAgent = rest.DefaultKubernetesUserAgent()
	}

	httpClient, err := rest.HTTPClientFor(&config)
	if err != nil {
		return nil, err
	}

	core, err := newRESTClient(config, httpClient, "/api", corev1.SchemeGroupVersion)
	if err != nil {
		return nil, fmt.Errorf("create core/v1 client: %w", err)
	}
	rbac, err := newRESTClient(config, httpClient, "/apis", rbacv1.SchemeGroupVersion)
	if err != nil {
		return nil, fmt.Errorf("create rbac/v1 client: %w", err)
	}

	return &Clientset{
		coreV1: &CoreV1Client{restClient: core},
		rbacV1: &RbacV1Client{restClient: rbac},
	}, nil
}

func newRESTClient(config rest.Config, httpClient *http.Client, apiPath string, gv schema.GroupVersion) (*rest.RESTClient, error) {
	config.APIPath = apiPath
	config.GroupVersion = &gv
	config.NegotiatedSerializer = Codecs.WithoutConversion()
	return rest.RESTClientForConfigAndClient(&config, httpClient)
}

// CoreV1 returns the client for the core/v1 API group.
func (c *Clientset) CoreV1() *CoreV1Client {
	return c.coreV1
}

// RbacV1 returns the client for the rbac.authorization.k8s.io/v1 API group.
func (c *Clientset) RbacV1() *RbacV1Client {
	return c.rbacV1
}

// CoreV1Client gives access to core/v1 resources.
type CoreV1Client struct {
	restClient rest.Interface
}

// RESTClient returns the underlying REST client, e.g. to build requests for subresources such as exec.
func (c *CoreV1Client) RESTClient() rest.Interface {
	return c.restClient
}

func (c *CoreV1Client) Namespaces() *Resource[corev1.Namespace, *corev1.Namespace] {
	return newResource[corev1.Namespace](c.restClient, "", "namespaces")
}

func (c *CoreV1Client) Nodes() *ListableResource[corev1.Node, *corev1.Node, corev1.NodeList, *corev1.NodeList] {
	return &ListableResource[corev1.Node, *corev1.Node, corev1.NodeList, *corev1.NodeList]{
		Resource: *newResource[corev1.Node](c.restClient, "", "nodes"),
	}
}

func (c *CoreV1Client) PersistentVolumeClaims(namespace string) *Resource[corev1.PersistentVolumeClaim, *corev1.PersistentVolumeClaim] {
	return newResource[corev1.PersistentVolumeClaim](c.restClient, namespace, "persistentvolumeclaims")
}

func (c *CoreV1Client) Pods(namespace string) *PodResource {
	return &PodResource{Resource: *newResource[corev1.Pod](c.restClient, namespace, "pods")}
}

func (c *CoreV1Client) Secrets(namespace string) *Resource[corev1.Secret, *corev1.Secret] {
	return newResource[corev1.Secret](c.restClient, namespace, "secrets")
}

func (c *CoreV1Client) ServiceAccounts(namespace string) *Resource[corev1.ServiceAccount, *corev1.ServiceAccount] {
	return newResource[corev1.ServiceAccount](c.restClient, namespace, "serviceaccounts")
}

// RbacV1Client gives access to rbac.authorization.k8s.io/v1 resources.
type RbacV1Client struct {
	restClient rest.Interface
}

func (c *RbacV1Client) RoleBindings(namespace string) *Resource[rbacv1.RoleBinding, *rbacv1.RoleBinding] {
	return newResource[rbacv1.RoleBinding](c.restClient, namespace, "rolebindings")
}

// object is a pointer to T that is also a runtime.Object, e.g. *corev1.Pod for corev1.Pod.
type object[T any] interface {
	*T
	runtime.Object
}

// Resource is a client for one resource type, in one namespace or cluster-scoped (empty namespace).
type Resource[T any, PT object[T]] struct {
	client    rest.Interface
	namespace string
	resource  string
}

func newResource[T any, PT object[T]](client rest.Interface, namespace, resource string) *Resource[T, PT] {
	return &Resource[T, PT]{client: client, namespace: namespace, resource: resource}
}

// Get returns the named object.
func (r *Resource[T, PT]) Get(ctx context.Context, name string, opts metav1.GetOptions) (*T, error) {
	result := PT(new(T))
	err := r.client.Get().
		NamespaceIfScoped(r.namespace, r.namespace != "").
		Resource(r.resource).
		Name(name).
		VersionedParams(&opts, ParameterCodec).
		Do(ctx).
		Into(result)
	return result, err
}

// Create creates obj and returns the server's representation of it.
func (r *Resource[T, PT]) Create(ctx context.Context, obj *T, opts metav1.CreateOptions) (*T, error) {
	result := PT(new(T))
	err := r.client.Post().
		NamespaceIfScoped(r.namespace, r.namespace != "").
		Resource(r.resource).
		VersionedParams(&opts, ParameterCodec).
		Body(PT(obj)).
		Do(ctx).
		Into(result)
	return result, err
}

// Delete deletes the named object.
func (r *Resource[T, PT]) Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error {
	return r.client.Delete().
		NamespaceIfScoped(r.namespace, r.namespace != "").
		Resource(r.resource).
		Name(name).
		Body(&opts).
		Do(ctx).
		Error()
}

// ListableResource is a Resource that can also be listed, with L being its list type.
type ListableResource[T any, PT object[T], L any, PL object[L]] struct {
	Resource[T, PT]
}

// List returns the objects that match opts.
func (r *ListableResource[T, PT, L, PL]) List(ctx context.Context, opts metav1.ListOptions) (*L, error) {
	var timeout time.Duration
	if opts.TimeoutSeconds != nil {
		timeout = time.Duration(*opts.TimeoutSeconds) * time.Second
	}
	result := PL(new(L))
	err := r.client.Get().
		NamespaceIfScoped(r.namespace, r.namespace != "").
		Resource(r.resource).
		VersionedParams(&opts, ParameterCodec).
		Timeout(timeout).
		Do(ctx).
		Into(result)
	return result, err
}

// PodResource is the client for pods.
type PodResource struct {
	Resource[corev1.Pod, *corev1.Pod]
}

// GetLogs returns a request for the logs of the named pod; call Stream or Do on it.
func (r *PodResource) GetLogs(name string, opts *corev1.PodLogOptions) *rest.Request {
	return r.client.Get().
		Namespace(r.namespace).
		Name(name).
		Resource(r.resource).
		SubResource("log").
		VersionedParams(opts, ParameterCodec)
}
