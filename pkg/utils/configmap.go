package utils

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	controllerConfigMapKey       = "disable-vpc-resource-controller"
	controllerConfigDisableValue = "true"
)

// GetControllerConfigMapId returns the id for the configmap resource containing the controller config
func GetControllerConfigMapId() types.NamespacedName {
	return types.NamespacedName{
		Namespace: "kube-system",
		Name:      "amazon-vpc-cni",
	}
}

// GetConfigmapCheckFn returns a function that checks if controller is enabled in the configmap
func GetConfigmapCheckFn() func(configMap *corev1.ConfigMap) bool {
	return func(configMap *corev1.ConfigMap) bool {
		return configMap.Data[controllerConfigMapKey] != controllerConfigDisableValue
	}
}
