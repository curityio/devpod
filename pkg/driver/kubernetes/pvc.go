package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/curityio/devpod/pkg/driver"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func (k *KubernetesDriver) createPersistentVolumeClaim(
	ctx context.Context,
	id string,
	options *driver.RunOptions,
) error {
	pvc, err := k.buildPersistentVolumeClaim(id, options)
	if err != nil {
		return err
	}

	k.Log.Infof("Create Persistent Volume Claim '%s'", id)
	_, err = k.client.Client().CoreV1().PersistentVolumeClaims(k.namespace).Create(ctx, pvc, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("create pvc: %w", err)
	}

	return nil
}

func (k *KubernetesDriver) buildPersistentVolumeClaim(
	id string,
	options *driver.RunOptions,
) (*corev1.PersistentVolumeClaim, error) {
	containerInfo, err := k.getDevContainerInformation(id, options)
	if err != nil {
		return nil, err
	}

	size := k.options.DiskSize
	quantity, err := resource.ParseQuantity(size)
	if err != nil {
		return nil, errors.Wrapf(err, "parse persistent volume size '%s'", size)
	}

	var storageClassName *string
	if k.options.StorageClass != "" {
		storageClassName = &k.options.StorageClass
	}
	accessMode := []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
	if k.options.PvcAccessMode != "" {
		switch k.options.PvcAccessMode {
		case "RWO":
			accessMode = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
		case "ROX":
			accessMode = []corev1.PersistentVolumeAccessMode{corev1.ReadOnlyMany}
		case "RWX":
			accessMode = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany}
		case "RWOP":
			accessMode = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOncePod}
		default:
			accessMode = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
		}
	}

	labels := map[string]string{}
	labels[DevPodWorkspaceUIDLabel] = options.UID
	for k, v := range ExtraDevPodLabels {
		labels[k] = v
	}

	annotations := map[string]string{}
	annotations[DevPodInfoAnnotation] = containerInfo
	extraAnnotations, err := parseLabels(k.options.PvcAnnotations)
	if err != nil {
		k.Log.Error("Failed to parse annotations from PVC_ANNOTATIONS option: %v", err)
	}
	for k, v := range extraAnnotations {
		annotations[k] = v
	}

	return &corev1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PersistentVolumeClaim",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        id,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: accessMode,
			Resources: corev1.VolumeResourceRequirements{
				Requests: map[corev1.ResourceName]resource.Quantity{
					corev1.ResourceStorage: quantity,
				},
			},
			StorageClassName: storageClassName,
		},
	}, nil
}

func (k *KubernetesDriver) getDevContainerInformation(
	id string,
	options *driver.RunOptions,
) (string, error) {
	containerInfo, err := json.Marshal(&DevContainerInfo{
		WorkspaceID: id,
		Options:     options,
	})
	if err != nil {
		return "", err
	}

	return string(containerInfo), nil
}

// updateDevContainerInfo replaces the run options kept on the workspace's persistent volume claim, which is where a
// later start of the workspace reads them from, unless they are the same.
func (k *KubernetesDriver) updateDevContainerInfo(ctx context.Context, pvc *corev1.PersistentVolumeClaim, options *driver.RunOptions) error {
	containerInfo, err := k.getDevContainerInformation(pvc.Name, options)
	if err != nil {
		return err
	} else if containerInfo == pvc.Annotations[DevPodInfoAnnotation] {
		return nil
	}

	k.Log.Debugf("Update the run options on persistent volume claim '%s'", pvc.Name)
	return k.setDevContainerInfo(ctx, pvc.Name, containerInfo)
}

// setDevContainerInfo patches the dev container info annotation alone, so that it doesn't conflict with the changes
// Kubernetes makes to the persistent volume claim.
func (k *KubernetesDriver) setDevContainerInfo(ctx context.Context, id, containerInfo string) error {
	patch, err := json.Marshal(map[string]any{
		"metadata": map[string]any{
			"annotations": map[string]string{DevPodInfoAnnotation: containerInfo},
		},
	})
	if err != nil {
		return err
	}

	_, err = k.client.Client().CoreV1().PersistentVolumeClaims(k.namespace).Patch(ctx, id, types.MergePatchType, patch, metav1.PatchOptions{})
	if kerrors.IsForbidden(err) {
		return fmt.Errorf("update persistent volume claim '%s': DevPod needs the permission to patch persistent volume claims in namespace '%s': %w", id, k.namespace, err)
	} else if err != nil {
		return fmt.Errorf("update persistent volume claim '%s': %w", id, err)
	}

	return nil
}
