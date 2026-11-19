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
		for _, node := range nodes {
			if path == nodesPath+"/"+node.Name {
				return http.StatusOK, node
			}
		}
		return http.StatusNotFound, kerrors.NewNotFound(schema.GroupResource{Resource: "nodes"}, strings.TrimPrefix(path, nodesPath+"/")).ErrStatus
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
			wantErr:  "get node 'gone-0' of the pod template",
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

func TestPodArchitecture(t *testing.T) {
	mixed := []corev1.Node{testNode("arm-0", "arm64"), testNode("workspace-0", "amd64")}
	armSelector := map[string]string{corev1.LabelArchStable: "arm64"}

	tests := []struct {
		name         string
		nodes        []corev1.Node
		architecture string
		nodeSelector map[string]string
		imageArch    string
		want         string
		wantErr      string
	}{
		{
			name:      "pins to the image's architecture",
			nodes:     []corev1.Node{testNode("arm-0", "arm64")},
			imageArch: "amd64",
			want:      "amd64",
		},
		{
			// the image of a workspace built before ARCHITECTURE changed only runs on its own architecture
			name:         "pins to the image's architecture over the ARCHITECTURE option",
			nodes:        mixed,
			architecture: "arm64",
			imageArch:    "amd64",
			want:         "amd64",
		},
		{
			name:         "pins to the ARCHITECTURE option",
			nodes:        mixed,
			architecture: "amd64",
			want:         "amd64",
		},
		{
			name:  "pins to the detected architecture",
			nodes: []corev1.Node{testNode("system-0", "arm64", tainted(criticalAddonsOnly)), testNode("workspace-0", "amd64")},
			want:  "amd64",
		},
		{
			name:  "doesn't pin when the architecture can't be detected",
			nodes: mixed,
			want:  "",
		},
		{
			name:         "keeps the user's node selector",
			nodes:        mixed,
			nodeSelector: armSelector,
			imageArch:    "arm64",
			want:         "",
		},
		{
			name:         "node selector conflicts with the ARCHITECTURE option",
			nodes:        mixed,
			architecture: "amd64",
			nodeSelector: armSelector,
			wantErr:      "conflicts with the ARCHITECTURE provider option amd64",
		},
		{
			name:         "node selector conflicts with the image's architecture",
			nodes:        mixed,
			nodeSelector: armSelector,
			imageArch:    "amd64",
			wantErr:      "the workspace's image was built for amd64, but the node selector kubernetes.io/arch=arm64",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			driver, _ := nodesDriver(t, tt.nodes...)
			driver.options.Architecture = tt.architecture
			nodeSelector := tt.nodeSelector
			if nodeSelector == nil {
				nodeSelector = map[string]string{}
			}

			arch, err := driver.podArchitecture(context.Background(), &corev1.Pod{}, nodeSelector, tt.imageArch)
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
