package kubernetes

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

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
		k.Log.Warnf("No node in the Kubernetes cluster can run the workspace pod right now, so its architecture is unknown. Set it with the ARCHITECTURE provider option")
	}

	return arch, nil
}

// chosenArchitecture returns the architecture chosen with a kubernetes.io/arch node selector or the ARCHITECTURE
// option, or "" if there is none. It fails if they differ, as the image would be built for one and run on the other.
func (k *KubernetesDriver) chosenArchitecture(nodeSelector map[string]string) (string, error) {
	selected := nodeSelector[corev1.LabelArchStable]
	if selected != "" && k.options.Architecture != "" && selected != k.options.Architecture {
		return "", fmt.Errorf("the node selector %s=%s conflicts with the ARCHITECTURE provider option %s, please remove one of them", corev1.LabelArchStable, selected, k.options.Architecture)
	}
	if selected != "" {
		return selected, nil
	}

	return k.options.Architecture, nil
}

// podArchitecture returns the architecture to pin the workspace pod to with a kubernetes.io/arch node selector, or ""
// to not pin it: the architecture the image was built for (imageArch, if known), else the one chosen with the
// ARCHITECTURE option, else the architecture of the nodes the pod can be scheduled on. A kubernetes.io/arch node
// selector is kept, as long as it matches the image. Unlike TargetArchitecture, it doesn't fail when the nodes'
// architecture can't be found out, as a workspace that wasn't built may use an image for several architectures.
func (k *KubernetesDriver) podArchitecture(ctx context.Context, pod *corev1.Pod, nodeSelector map[string]string, imageArch string) (string, error) {
	chosen, err := k.chosenArchitecture(nodeSelector)
	if err != nil {
		return "", err
	}

	if selected := nodeSelector[corev1.LabelArchStable]; selected != "" {
		if imageArch != "" && imageArch != selected {
			return "", fmt.Errorf("the workspace's image was built for %s, but the node selector %s=%s runs it on %s nodes. Rebuild the workspace with `devpod up --recreate`", imageArch, corev1.LabelArchStable, selected, selected)
		}
		return "", nil
	}

	if imageArch != "" {
		if chosen != "" && chosen != imageArch {
			k.Log.Warnf("The workspace's image was built for %s, not for the ARCHITECTURE provider option %s, so it keeps running on %s nodes. Rebuild the workspace with `devpod up --recreate` to use %s", imageArch, chosen, imageArch, chosen)
		}
		return imageArch, nil
	}
	if chosen != "" {
		return chosen, nil
	}

	arch, err := k.schedulableArchitecture(ctx, pod, nodeSelector)
	if err != nil {
		k.Log.Debugf("Not pinning the workspace pod to an architecture: %v", err)
		return "", nil
	}

	return arch, nil
}

// schedulableArchitecture returns the architecture of the nodes that the pod, with the given node selector, can be
// scheduled on, or "" if there are none. It fails if those nodes have different architectures. A pod template that
// names its node with spec.nodeName bypasses the scheduler, so then it's that node's architecture.
func (k *KubernetesDriver) schedulableArchitecture(ctx context.Context, pod *corev1.Pod, nodeSelector map[string]string) (string, error) {
	if pod.Spec.NodeName != "" {
		node, err := k.client.Client().CoreV1().Nodes().Get(ctx, pod.Spec.NodeName, metav1.GetOptions{})
		if err != nil {
			return "", nodesError(fmt.Sprintf("get node '%s' of the pod template", pod.Spec.NodeName), err)
		}

		return node.Status.NodeInfo.Architecture, nil
	}

	nodes, err := k.client.Client().CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", nodesError("list nodes", err)
	}

	nodesByArch := map[string][]string{}
	for i := range nodes.Items {
		node := &nodes.Items[i]
		if reason := unschedulableReason(node, pod, nodeSelector); reason != "" {
			k.Log.Debugf("Workspace pod can't run on node '%s': %s", node.Name, reason)
			continue
		}

		arch := node.Status.NodeInfo.Architecture
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

func nodesError(action string, err error) error {
	if kerrors.IsForbidden(err) {
		return fmt.Errorf("you don't have permission to read nodes in the Kubernetes cluster, please set the cluster architecture manually with the ARCHITECTURE provider option")
	}

	return fmt.Errorf("%s: %w", action, err)
}

// unschedulableReason returns why the pod, with the given node selector, can't be scheduled on the node, or "" if it
// can. It only looks at what doesn't depend on the other pods: whether the node is ready and schedulable, the pod's
// node selector and required node affinity, and the node's taints.
func unschedulableReason(node *corev1.Node, pod *corev1.Pod, nodeSelector map[string]string) string {
	if node.Spec.Unschedulable {
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
