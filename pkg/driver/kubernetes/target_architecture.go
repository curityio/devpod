package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/curityio/devpod/pkg/driver"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
)

// TargetArchitecture returns the architecture to build the workspace's image for: the one chosen with the
// ARCHITECTURE option or a kubernetes.io/arch node selector, or else the architecture of the nodes the workspace pod
// can be scheduled on.
func (k *KubernetesDriver) TargetArchitecture(ctx context.Context, workspaceId string) (string, error) {
	k.Log.Debugf("Getting target architecture for workspace '%s'", workspaceId)
	defer k.Log.Debugf("Done getting target architecture for workspace '%s'", workspaceId)

	pod, err := k.getPodTemplate()
	if err != nil {
		return "", err
	}
	nodeSelector, err := getNodeSelector(pod, k.options.NodeSelector)
	if err != nil {
		return "", err
	}
	chosen, err := k.chosenArchitecture(nodeSelector)
	if err != nil {
		return "", err
	} else if chosen != "" {
		return chosen, nil
	}

	arch, err := k.schedulableArchitecture(ctx, pod, nodeSelector)
	if err != nil {
		return "", err
	} else if arch == "" {
		k.Log.Warnf("No node in the Kubernetes cluster can run the workspace pod right now, so the workspace gets the architecture of the node its pod starts on. Set the ARCHITECTURE provider option to choose it")
	}

	return arch, nil
}

// chosenArchitecture returns the architecture chosen with a kubernetes.io/arch node selector or the ARCHITECTURE
// option, or "" if there is none. It fails if they differ, as the image would be built for one and run on the other,
// and if it isn't an architecture a node can have, as the pod is pinned to it and would never be scheduled.
func (k *KubernetesDriver) chosenArchitecture(nodeSelector map[string]string) (string, error) {
	selected := nodeSelector[corev1.LabelArchStable]
	if selected != "" && k.options.Architecture != "" && selected != k.options.Architecture {
		return "", fmt.Errorf("the node selector %s=%s conflicts with the ARCHITECTURE provider option %s, please remove one of them", corev1.LabelArchStable, selected, k.options.Architecture)
	}
	if selected != "" {
		if !slices.Contains(nodeArchitectures, selected) {
			return "", fmt.Errorf("the node selector %s=%s doesn't name an architecture of Kubernetes nodes, such as amd64 or arm64", corev1.LabelArchStable, selected)
		}
		return selected, nil
	}

	if k.options.Architecture != "" && !slices.Contains(nodeArchitectures, k.options.Architecture) {
		return "", fmt.Errorf("the ARCHITECTURE provider option %s isn't an architecture of Kubernetes nodes, such as amd64 or arm64", k.options.Architecture)
	}

	return k.options.Architecture, nil
}

// nodeArchitectures are the values a node's kubernetes.io/arch label can have: the architectures Go supports on Linux.
var nodeArchitectures = []string{"386", "amd64", "arm", "arm64", "loong64", "mips", "mips64", "mips64le", "mipsle", "ppc64", "ppc64le", "riscv64", "s390x"}

// podArchitecture returns the architecture to pin the workspace pod to with a kubernetes.io/arch node selector, or ""
// to not pin it: the workspace's architecture (workspaceArch, if known), else the one chosen with the ARCHITECTURE
// option. A kubernetes.io/arch node selector is kept, as long as it matches the workspace. The architecture that
// TargetArchitecture finds from the nodes isn't used: it's a guess from the nodes there are now, which is needed to
// look for a prebuilt image, but the pod of a workspace without one can run on any node, and recordArchitecture then
// keeps that node's architecture.
func (k *KubernetesDriver) podArchitecture(nodeSelector map[string]string, workspaceArch string) (string, error) {
	chosen, err := k.chosenArchitecture(nodeSelector)
	if err != nil {
		return "", err
	}

	if selected := nodeSelector[corev1.LabelArchStable]; selected != "" {
		if workspaceArch != "" && workspaceArch != selected {
			return "", fmt.Errorf("the workspace was built for %s, but the node selector %s=%s runs it on %s nodes. Rebuild the workspace with `devpod up --recreate`", workspaceArch, corev1.LabelArchStable, selected, selected)
		}
		return "", nil
	}

	if workspaceArch != "" {
		if chosen != "" && chosen != workspaceArch {
			k.Log.Warnf("The workspace was built for %s, not for the ARCHITECTURE provider option %s, so it keeps running on %s nodes. Rebuild the workspace with `devpod up --recreate` to use %s", workspaceArch, chosen, workspaceArch, chosen)
		}
		return workspaceArch, nil
	}

	return chosen, nil
}

// recordArchitecture sets the architecture of the node the workspace pod runs on in the run options, and keeps it
// with the workspace, when the run options don't have one: there was no prebuilt image for the workspace, or an older
// DevPod created it. Later pods of the workspace are pinned to that architecture, as the workspace's volume holds what
// was built on it or copied from its image. It only trusts a pod whose dev container runs: one where it doesn't may
// be on a node of an architecture its image doesn't run on. Not being able to record it doesn't fail the workspace:
// the next start tries again.
func (k *KubernetesDriver) recordArchitecture(ctx context.Context, id string, options *driver.RunOptions) {
	if options.Architecture != "" {
		return
	}

	arch, err := k.runningArchitecture(ctx, id)
	if errors.Is(err, errNodesForbidden) {
		k.Log.Debugf("Can't find out the architecture workspace pod '%s' runs on: %v", id, err)
		return
	} else if err != nil {
		k.Log.Warnf("Couldn't find out the architecture workspace pod '%s' runs on, so its next start may be on another one: %v", id, err)
		return
	} else if arch == "" {
		return
	}

	options.Architecture = arch
	containerInfo, err := k.getDevContainerInformation(id, options)
	if err == nil {
		err = k.setDevContainerInfo(ctx, id, containerInfo)
	}
	if err != nil {
		k.Log.Warnf("Couldn't keep the architecture %s with the workspace, so its next start may be on another one: %v", arch, err)
		return
	}

	k.Log.Debugf("Workspace pod '%s' runs on %s, keeping that with the workspace", id, arch)
}

// runningArchitecture returns the architecture of the node the workspace's dev container runs on, or "" if it isn't
// running or its node doesn't tell.
func (k *KubernetesDriver) runningArchitecture(ctx context.Context, id string) (string, error) {
	pod, err := k.getPod(ctx, id)
	if err != nil {
		return "", err
	} else if pod == nil || pod.Spec.NodeName == "" || !devContainerRunning(pod) {
		return "", nil
	}

	// the node of a running pod matches the pod's node selector
	if arch := pod.Spec.NodeSelector[corev1.LabelArchStable]; arch != "" {
		return arch, nil
	}

	node, err := k.getNode(ctx, pod.Spec.NodeName)
	if err != nil {
		return "", err
	}

	return nodeArchitecture(node), nil
}

// devContainerRunning reports whether the pod's dev container is running. The pod's phase doesn't tell: it's Running
// as long as one of its containers is, so also with a sidecar of the pod template next to a dev container that
// couldn't start.
func devContainerRunning(pod *corev1.Pod) bool {
	if !isPodRunning(pod) {
		return false
	}

	for _, c := range pod.Status.ContainerStatuses {
		if c.Name == DevContainerName {
			return c.State.Running != nil
		}
	}

	return false
}

// schedulableArchitecture returns the architecture of the nodes that the pod, with the given node selector, can be
// scheduled on, or "" if there are none. It fails if those nodes have different architectures. A pod template that
// names its node with spec.nodeName bypasses the scheduler, so then it's that node's architecture.
func (k *KubernetesDriver) schedulableArchitecture(ctx context.Context, pod *corev1.Pod, nodeSelector map[string]string) (string, error) {
	if pod.Spec.NodeName != "" {
		node, err := k.getNode(ctx, pod.Spec.NodeName)
		if err != nil {
			return "", fmt.Errorf("find the node of the pod template: %w", err)
		}

		return nodeArchitecture(node), nil
	}

	nodes, err := k.client.Client().CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", nodesError(err)
	}

	nodesByArch := map[string][]string{}
	for i := range nodes.Items {
		node := &nodes.Items[i]
		if reason := unschedulableReason(node, pod, nodeSelector); reason != "" {
			k.Log.Debugf("Workspace pod can't run on node '%s': %s", node.Name, reason)
			continue
		}

		arch := nodeArchitecture(node)
		if arch == "" {
			k.Log.Debugf("Node '%s' doesn't tell its architecture", node.Name)
			continue
		}
		nodesByArch[arch] = append(nodesByArch[arch], node.Name)
	}

	if len(nodesByArch) > 1 {
		archs := []string{}
		for arch, names := range nodesByArch {
			archs = append(archs, fmt.Sprintf("%s (%s)", arch, strings.Join(names, ", ")))
		}
		sort.Strings(archs)
		return "", fmt.Errorf("the workspace pod can run on nodes of different architectures: %s. Please choose one with the ARCHITECTURE provider option, or restrict the nodes with NODE_SELECTOR", strings.Join(archs, ", "))
	}

	for arch := range nodesByArch {
		return arch, nil
	}

	return "", nil
}

// getNode returns the named node. It lists the nodes with a field selector instead of getting the node, so that
// listing nodes stays the only permission on nodes that DevPod uses.
func (k *KubernetesDriver) getNode(ctx context.Context, name string) (*corev1.Node, error) {
	nodes, err := k.client.Client().CoreV1().Nodes().List(ctx, metav1.ListOptions{FieldSelector: "metadata.name=" + name})
	if err != nil {
		return nil, nodesError(err)
	}

	for i := range nodes.Items {
		if nodes.Items[i].Name == name {
			return &nodes.Items[i], nil
		}
	}

	return nil, fmt.Errorf("node '%s' doesn't exist", name)
}

// nodeArchitecture returns the node's architecture, e.g. amd64: its kubernetes.io/arch label, which is what the
// pod's node selector matches, or else what the kubelet reports.
func nodeArchitecture(node *corev1.Node) string {
	if arch := node.Labels[corev1.LabelArchStable]; arch != "" {
		return arch
	}

	return node.Status.NodeInfo.Architecture
}

var errNodesForbidden = errors.New("you don't have permission to list nodes in the Kubernetes cluster, please set the cluster architecture manually with the ARCHITECTURE provider option")

func nodesError(err error) error {
	if kerrors.IsForbidden(err) {
		return errNodesForbidden
	}

	return fmt.Errorf("list nodes: %w", err)
}

// unschedulableReason returns why the pod, with the given node selector, can't be scheduled on the node, or "" if it
// can. It only looks at what doesn't depend on the other pods: whether the node is ready and not cordoned, the pod's
// node selector and required node affinity, and the node's taints.
func unschedulableReason(node *corev1.Node, pod *corev1.Pod, nodeSelector map[string]string) string {
	// the scheduler treats a cordoned node as one with this taint
	if node.Spec.Unschedulable && !toleratesTaint(pod.Spec.Tolerations, &corev1.Taint{Key: corev1.TaintNodeUnschedulable, Effect: corev1.TaintEffectNoSchedule}) {
		return "it is cordoned"
	}
	if !nodeReady(node) {
		return "it isn't ready"
	}
	if !labels.SelectorFromSet(nodeSelector).Matches(labels.Set(node.Labels)) {
		return "it doesn't match the node selector"
	}
	if !matchesRequiredNodeAffinity(node, pod.Spec.Affinity) {
		return "it doesn't match the pod's required node affinity"
	}
	for i := range node.Spec.Taints {
		taint := &node.Spec.Taints[i]
		if taint.Effect != corev1.TaintEffectNoSchedule && taint.Effect != corev1.TaintEffectNoExecute {
			continue
		}
		if !toleratesTaint(pod.Spec.Tolerations, taint) {
			return fmt.Sprintf("the pod doesn't tolerate its taint %s", taint.ToString())
		}
	}

	return ""
}

func nodeReady(node *corev1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			return condition.Status == corev1.ConditionTrue
		}
	}

	return false
}

func toleratesTaint(tolerations []corev1.Toleration, taint *corev1.Taint) bool {
	for i := range tolerations {
		if tolerations[i].ToleratesTaint(taint) {
			return true
		}
	}

	return false
}

// matchesRequiredNodeAffinity reports whether the node matches the affinity's requiredDuringScheduling node selector
// terms: any one of them, which in turn needs all its requirements.
func matchesRequiredNodeAffinity(node *corev1.Node, affinity *corev1.Affinity) bool {
	if affinity == nil || affinity.NodeAffinity == nil || affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		return true
	}

	for _, term := range affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms {
		// an empty term matches no node
		if len(term.MatchExpressions) == 0 && len(term.MatchFields) == 0 {
			continue
		}
		if nodeSelectorRequirementsMatch(term.MatchExpressions, labels.Set(node.Labels)) &&
			nodeFieldRequirementsMatch(term.MatchFields, node) {
			return true
		}
	}

	return false
}

var nodeSelectorOperators = map[corev1.NodeSelectorOperator]selection.Operator{
	corev1.NodeSelectorOpIn:           selection.In,
	corev1.NodeSelectorOpNotIn:        selection.NotIn,
	corev1.NodeSelectorOpExists:       selection.Exists,
	corev1.NodeSelectorOpDoesNotExist: selection.DoesNotExist,
	corev1.NodeSelectorOpGt:           selection.GreaterThan,
	corev1.NodeSelectorOpLt:           selection.LessThan,
}

func nodeSelectorRequirementsMatch(requirements []corev1.NodeSelectorRequirement, set labels.Set) bool {
	for _, r := range requirements {
		op, ok := nodeSelectorOperators[r.Operator]
		if !ok {
			return false
		}
		requirement, err := labels.NewRequirement(r.Key, op, r.Values)
		if err != nil || !requirement.Matches(set) {
			return false
		}
	}

	return true
}

// nodeFieldRequirementsMatch matches the node's fields against a node selector term's matchFields, which Kubernetes
// only allows for metadata.name with the In and NotIn operators. Unlike label values, a node name may be longer than
// 63 characters, so they are compared as they are.
func nodeFieldRequirementsMatch(requirements []corev1.NodeSelectorRequirement, node *corev1.Node) bool {
	for _, r := range requirements {
		if r.Key != "metadata.name" {
			return false
		}

		switch r.Operator {
		case corev1.NodeSelectorOpIn:
			if !slices.Contains(r.Values, node.Name) {
				return false
			}
		case corev1.NodeSelectorOpNotIn:
			if slices.Contains(r.Values, node.Name) {
				return false
			}
		case corev1.NodeSelectorOpExists, corev1.NodeSelectorOpDoesNotExist, corev1.NodeSelectorOpGt, corev1.NodeSelectorOpLt:
			return false
		default:
			return false
		}
	}

	return true
}
