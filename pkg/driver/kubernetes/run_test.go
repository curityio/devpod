package kubernetes

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDeadPodReason(t *testing.T) {
	running := corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}
	waiting := corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"}}
	killed := corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 255, Reason: "Unknown"}}
	now := metav1.Now()

	tests := []struct {
		name string
		pod  corev1.Pod
		dead bool
	}{
		{
			name: "running pod is reused",
			pod:  pod(corev1.PodRunning, containerStatus(DevContainerName, running)),
		},
		{
			name: "pending pod is reused",
			pod:  pod(corev1.PodPending, containerStatus(DevContainerName, waiting)),
		},
		{
			name: "pending pod without container statuses is reused",
			pod:  pod(corev1.PodPending),
		},
		{
			name: "terminated sidecar doesn't make the pod dead",
			pod:  pod(corev1.PodRunning, containerStatus("sidecar", killed), containerStatus(DevContainerName, running)),
		},
		{
			// what a node restart leaves behind
			name: "failed pod with killed container is dead",
			pod:  pod(corev1.PodFailed, containerStatus(DevContainerName, killed)),
			dead: true,
		},
		{
			name: "failed pod is dead",
			pod:  pod(corev1.PodFailed),
			dead: true,
		},
		{
			name: "succeeded pod is dead",
			pod:  pod(corev1.PodSucceeded),
			dead: true,
		},
		{
			name: "pod in unknown phase is dead",
			pod:  pod(corev1.PodUnknown),
			dead: true,
		},
		{
			name: "running pod with terminated dev container is dead",
			pod:  pod(corev1.PodRunning, containerStatus(DevContainerName, killed)),
			dead: true,
		},
		{
			name: "terminating pod is dead",
			pod: func() corev1.Pod {
				p := pod(corev1.PodRunning, containerStatus(DevContainerName, running))
				p.DeletionTimestamp = &now
				return p
			}(),
			dead: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason := deadPodReason(&tt.pod)
			if tt.dead && reason == "" {
				t.Errorf("deadPodReason() = \"\", want a reason")
			} else if !tt.dead && reason != "" {
				t.Errorf("deadPodReason() = %q, want \"\"", reason)
			}
		})
	}
}

func pod(phase corev1.PodPhase, statuses ...corev1.ContainerStatus) corev1.Pod {
	return corev1.Pod{Status: corev1.PodStatus{Phase: phase, ContainerStatuses: statuses}}
}

func containerStatus(name string, state corev1.ContainerState) corev1.ContainerStatus {
	return corev1.ContainerStatus{Name: name, State: state}
}
