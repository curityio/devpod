package kubernetes

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"gotest.tools/assert"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const nodesPath = "/api/v1/nodes"

// what AKS gives the nodes of a system node pool
var criticalAddonsOnly = corev1.Taint{Key: "CriticalAddonsOnly", Value: "true", Effect: corev1.TaintEffectNoSchedule}

type nodeOption func(*corev1.Node)

func testNode(name, arch string, opts ...nodeOption) corev1.Node {
	node := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{corev1.LabelArchStable: arch, "agentpool": strings.Split(name, "-")[0]},
		},
		Status: corev1.NodeStatus{
			NodeInfo:   corev1.NodeSystemInfo{Architecture: arch},
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
	for _, opt := range opts {
		opt(&node)
	}
	return node
}

func tainted(taint corev1.Taint) nodeOption {
	return func(n *corev1.Node) { n.Spec.Taints = append(n.Spec.Taints, taint) }
}

func cordoned(n *corev1.Node) { n.Spec.Unschedulable = true }

func notReady(n *corev1.Node) {
	n.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionFalse}}
}

func labeled(key, value string) nodeOption {
	return func(n *corev1.Node) { n.Labels[key] = value }
}

func nodesDriver(t *testing.T, nodes ...corev1.Node) (*KubernetesDriver, func() []string) {
	t.Helper()
	return fakeDriver(t, func(method, path string) (int, any) {
		if method != http.MethodGet {
			return http.StatusMethodNotAllowed, nil
		}
		if path == nodesPath {
			return http.StatusOK, corev1.NodeList{Items: nodes}
		}
		return http.StatusNotFound, nil
	})
}

func TestTargetArchitecture(t *testing.T) {
	system := testNode("system-0", "arm64", tainted(criticalAddonsOnly))
	workspace := testNode("workspace-0", "amd64")
	workspace2 := testNode("workspace-1", "amd64")

	tests := []struct {
		name         string
		nodes        []corev1.Node
		nodeSelector string
		template     string
		want         string
		wantErr      string
	}{
		{
			name:  "same architecture",
			nodes: []corev1.Node{workspace, workspace2},
			want:  "amd64",
		},
		{
			name:  "ignores nodes with a taint the pod doesn't tolerate",
			nodes: []corev1.Node{system, workspace, workspace2},
			want:  "amd64",
		},
		{
			name:  "ignores NoExecute taints the pod doesn't tolerate",
			nodes: []corev1.Node{testNode("system-0", "arm64", tainted(corev1.Taint{Key: "dedicated", Effect: corev1.TaintEffectNoExecute})), workspace},
			want:  "amd64",
		},
		{
			name:  "counts nodes with a PreferNoSchedule taint",
			nodes: []corev1.Node{testNode("system-0", "arm64", tainted(corev1.Taint{Key: "dedicated", Effect: corev1.TaintEffectPreferNoSchedule})), workspace},
			wantErr: "the workspace pod can run on nodes of different architectures: " +
				"amd64 (workspace-0), arm64 (system-0)",
		},
		{
			name:     "counts nodes with a taint the pod template tolerates",
			nodes:    []corev1.Node{system, workspace},
			template: "spec:\n  tolerations:\n  - key: CriticalAddonsOnly\n    operator: Exists\n",
			wantErr:  "different architectures",
		},
		{
			name:  "ignores cordoned and not ready nodes",
			nodes: []corev1.Node{testNode("old-0", "arm64", cordoned), testNode("old-1", "arm64", notReady), workspace},
			want:  "amd64",
		},
		{
			// the scheduler treats a cordoned node as tainted, and this pod tolerates every taint
			name:     "counts cordoned nodes the pod template tolerates",
			nodes:    []corev1.Node{testNode("old-0", "arm64", cordoned), workspace},
			template: "spec:\n  tolerations:\n  - operator: Exists\n",
			wantErr:  "amd64 (workspace-0), arm64 (old-0)",
		},
		{
			name: "ignores nodes that don't tell their architecture",
			nodes: []corev1.Node{testNode("new-0", "", func(n *corev1.Node) {
				delete(n.Labels, corev1.LabelArchStable)
			}), workspace},
			want: "amd64",
		},
		{
			name: "uses the architecture the kubelet reports for a node without the architecture label",
			nodes: []corev1.Node{testNode("new-0", "arm64", func(n *corev1.Node) {
				delete(n.Labels, corev1.LabelArchStable)
			})},
			want: "arm64",
		},
		{
			name:         "ignores nodes the NODE_SELECTOR option doesn't match",
			nodes:        []corev1.Node{testNode("arm-0", "arm64"), workspace},
			nodeSelector: "agentpool=workspace",
			want:         "amd64",
		},
		{
			name:     "ignores nodes the pod template's node selector doesn't match",
			nodes:    []corev1.Node{testNode("arm-0", "arm64"), workspace},
			template: "spec:\n  nodeSelector:\n    agentpool: arm\n",
			want:     "arm64",
		},
		{
			name:  "ignores nodes the pod template's required node affinity doesn't match",
			nodes: []corev1.Node{testNode("arm-0", "arm64"), testNode("gpu-0", "amd64", labeled("gpu", "true"))},
			template: `spec:
  affinity:
    nodeAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
        nodeSelectorTerms:
        - matchExpressions:
          - key: gpu
            operator: In
            values: ["true"]
`,
			want: "amd64",
		},
		{
			// the scheduler doesn't check taints or cordoning for a pod that names its node
			name:     "uses the architecture of the node the pod template names",
			nodes:    []corev1.Node{testNode("system-0", "arm64", tainted(criticalAddonsOnly), cordoned), workspace},
			template: "spec:\n  nodeName: system-0\n",
			want:     "arm64",
		},
		{
			name:     "node the pod template names doesn't exist",
			nodes:    []corev1.Node{workspace},
			template: "spec:\n  nodeName: gone-0\n",
			wantErr:  "find the node of the pod template: node 'gone-0' doesn't exist",
		},
		{
			name:         "uses the node selector's architecture",
			nodes:        []corev1.Node{testNode("arm-0", "arm64"), workspace},
			nodeSelector: corev1.LabelArchStable + "=arm64",
			want:         "arm64",
		},
		{
			name:  "no node the pod can run on",
			nodes: []corev1.Node{system},
			want:  "",
		},
		{
			name:    "mixed architectures",
			nodes:   []corev1.Node{testNode("arm-0", "arm64"), workspace, workspace2},
			wantErr: "amd64 (workspace-0, workspace-1), arm64 (arm-0)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			driver, _ := nodesDriver(t, tt.nodes...)
			driver.options.NodeSelector = tt.nodeSelector
			driver.options.PodManifestTemplate = tt.template

			arch, err := driver.TargetArchitecture(context.Background(), "ws")
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, arch, tt.want)
		})
	}
}

func TestTargetArchitectureOption(t *testing.T) {
	driver, requests := nodesDriver(t, testNode("arm-0", "arm64"), testNode("workspace-0", "amd64"))
	driver.options.Architecture = "amd64"

	arch, err := driver.TargetArchitecture(context.Background(), "ws")
	assert.NilError(t, err)
	assert.Equal(t, arch, "amd64")
	assert.Equal(t, len(requests()), 0)
}

func TestTargetArchitectureForbidden(t *testing.T) {
	driver, _ := fakeDriver(t, func(method, path string) (int, any) {
		return http.StatusForbidden, kerrors.NewForbidden(schema.GroupResource{Resource: "nodes"}, "", nil).ErrStatus
	})

	_, err := driver.TargetArchitecture(context.Background(), "ws")
	assert.ErrorContains(t, err, "ARCHITECTURE provider option")
}

func TestTargetArchitectureConflict(t *testing.T) {
	driver, _ := nodesDriver(t, testNode("arm-0", "arm64"))
	driver.options.Architecture = "amd64"
	driver.options.NodeSelector = corev1.LabelArchStable + "=arm64"

	_, err := driver.TargetArchitecture(context.Background(), "ws")
	assert.ErrorContains(t, err, "the node selector kubernetes.io/arch=arm64 conflicts with the ARCHITECTURE provider option amd64")
}

func TestTargetArchitectureUnknown(t *testing.T) {
	driver, _ := nodesDriver(t, testNode("workspace-0", "amd64"))
	driver.options.Architecture = "x86_64"
	_, err := driver.TargetArchitecture(context.Background(), "ws")
	assert.ErrorContains(t, err, "the ARCHITECTURE provider option x86_64 isn't an architecture of Kubernetes nodes")

	driver.options.Architecture = ""
	driver.options.NodeSelector = corev1.LabelArchStable + "=aarch64"
	_, err = driver.TargetArchitecture(context.Background(), "ws")
	assert.ErrorContains(t, err, "the node selector kubernetes.io/arch=aarch64 doesn't name an architecture of Kubernetes nodes")
}

func TestPodArchitecture(t *testing.T) {
	armSelector := map[string]string{corev1.LabelArchStable: "arm64"}

	tests := []struct {
		name          string
		architecture  string
		nodeSelector  map[string]string
		workspaceArch string
		want          string
		wantErr       string
	}{
		{
			name:          "pins to the workspace's architecture",
			workspaceArch: "amd64",
			want:          "amd64",
		},
		{
			// the image of a workspace built before ARCHITECTURE changed only runs on its own architecture
			name:          "pins to the workspace's architecture over the ARCHITECTURE option",
			architecture:  "arm64",
			workspaceArch: "amd64",
			want:          "amd64",
		},
		{
			name:         "pins to the ARCHITECTURE option",
			architecture: "amd64",
			want:         "amd64",
		},
		{
			// the pod can run on any node, and the workspace keeps the architecture of the one it runs on
			name: "doesn't pin when the architecture isn't known",
			want: "",
		},
		{
			name:          "keeps the user's node selector",
			nodeSelector:  armSelector,
			workspaceArch: "arm64",
			want:          "",
		},
		{
			name:         "node selector conflicts with the ARCHITECTURE option",
			architecture: "amd64",
			nodeSelector: armSelector,
			wantErr:      "conflicts with the ARCHITECTURE provider option amd64",
		},
		{
			name:          "node selector conflicts with the workspace's architecture",
			nodeSelector:  armSelector,
			workspaceArch: "amd64",
			wantErr:       "the workspace was built for amd64, but the node selector kubernetes.io/arch=arm64",
		},
		{
			name:         "ARCHITECTURE option that no node can have",
			architecture: "x86_64",
			wantErr:      "the ARCHITECTURE provider option x86_64 isn't an architecture of Kubernetes nodes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// podArchitecture must not need the nodes
			driver, requests := nodesDriver(t)
			driver.options.Architecture = tt.architecture

			arch, err := driver.podArchitecture(tt.nodeSelector, tt.workspaceArch)
			assert.Equal(t, len(requests()), 0)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, arch, tt.want)
		})
	}
}

func TestMatchesRequiredNodeAffinity(t *testing.T) {
	node := testNode("workspace-0", "amd64", labeled("cpus", "16"))
	// node names may be longer than the 63 characters a label value may have
	longName := "workspace-" + strings.Repeat("a", 70)
	longNode := testNode(longName, "amd64")
	nameField := func(op corev1.NodeSelectorOperator, names ...string) *corev1.Affinity {
		return &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{
				MatchFields: []corev1.NodeSelectorRequirement{{Key: "metadata.name", Operator: op, Values: names}},
			}}},
		}}
	}
	for _, tt := range []struct {
		name     string
		affinity *corev1.Affinity
		want     bool
	}{
		{name: "In with a long node name", affinity: nameField(corev1.NodeSelectorOpIn, longName), want: true},
		{name: "NotIn with a long node name", affinity: nameField(corev1.NodeSelectorOpNotIn, longName)},
		{name: "NotIn another node", affinity: nameField(corev1.NodeSelectorOpNotIn, "other"), want: true},
		{name: "unsupported operator", affinity: nameField(corev1.NodeSelectorOpExists)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, matchesRequiredNodeAffinity(&longNode, tt.affinity), tt.want)
		})
	}
	required := func(terms ...corev1.NodeSelectorTerm) *corev1.Affinity {
		return &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{NodeSelectorTerms: terms},
		}}
	}
	expr := func(key string, op corev1.NodeSelectorOperator, values ...string) corev1.NodeSelectorTerm {
		return corev1.NodeSelectorTerm{MatchExpressions: []corev1.NodeSelectorRequirement{{Key: key, Operator: op, Values: values}}}
	}

	tests := []struct {
		name     string
		affinity *corev1.Affinity
		want     bool
	}{
		{name: "no affinity", want: true},
		{name: "only preferred affinity", affinity: &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{}}, want: true},
		{name: "In", affinity: required(expr(corev1.LabelArchStable, corev1.NodeSelectorOpIn, "amd64")), want: true},
		{name: "In without match", affinity: required(expr(corev1.LabelArchStable, corev1.NodeSelectorOpIn, "arm64"))},
		{name: "NotIn", affinity: required(expr(corev1.LabelArchStable, corev1.NodeSelectorOpNotIn, "arm64")), want: true},
		{name: "DoesNotExist", affinity: required(expr("gpu", corev1.NodeSelectorOpDoesNotExist)), want: true},
		{name: "Exists without label", affinity: required(expr("gpu", corev1.NodeSelectorOpExists))},
		{name: "Gt", affinity: required(expr("cpus", corev1.NodeSelectorOpGt, "8")), want: true},
		{name: "Lt", affinity: required(expr("cpus", corev1.NodeSelectorOpLt, "8"))},
		{
			name:     "any term matches",
			affinity: required(expr("gpu", corev1.NodeSelectorOpExists), expr(corev1.LabelArchStable, corev1.NodeSelectorOpIn, "amd64")),
			want:     true,
		},
		{name: "empty term matches nothing", affinity: required(corev1.NodeSelectorTerm{})},
		{
			name: "match fields",
			affinity: required(corev1.NodeSelectorTerm{MatchFields: []corev1.NodeSelectorRequirement{
				{Key: "metadata.name", Operator: corev1.NodeSelectorOpIn, Values: []string{"workspace-0"}},
			}}),
			want: true,
		},
		{
			name: "match fields without match",
			affinity: required(corev1.NodeSelectorTerm{MatchFields: []corev1.NodeSelectorRequirement{
				{Key: "metadata.name", Operator: corev1.NodeSelectorOpIn, Values: []string{"other"}},
			}}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, matchesRequiredNodeAffinity(&node, tt.affinity), tt.want)
		})
	}
}
