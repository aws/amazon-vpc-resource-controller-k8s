package manifest

import (
    "github.com/aws/aws-sdk-go-v2/aws"
    appsv1 "k8s.io/api/apps/v1"
    corev1 "k8s.io/api/core/v1"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type StatefulSetBuilder struct {
    namespace              string
    name                   string
    serviceName            string
    replicas               int32
    labels                 map[string]string
    container             corev1.Container
    nodeSelector          map[string]string
    terminationGracePeriod int64
}

func NewDefaultStatefulSetBuilder() *StatefulSetBuilder {
    return &StatefulSetBuilder{
        namespace:              "default",
        name:                   "statefulset",
        serviceName:           "statefulset",
        replicas:               2,
        labels:                 map[string]string{},
        container:             NewBusyBoxContainerBuilder().Build(),
        nodeSelector:          map[string]string{"kubernetes.io/os": "linux"},
        terminationGracePeriod: 0,
    }
}

func (s *StatefulSetBuilder) Namespace(namespace string) *StatefulSetBuilder {
    s.namespace = namespace
    return s
}

func (s *StatefulSetBuilder) Name(name string) *StatefulSetBuilder {
    s.name = name
    s.serviceName = name // typically same as name
    return s
}

func (s *StatefulSetBuilder) ServiceName(serviceName string) *StatefulSetBuilder {
    s.serviceName = serviceName
    return s
}

func (s *StatefulSetBuilder) Replicas(replicas int32) *StatefulSetBuilder {
    s.replicas = replicas
    return s
}

func (s *StatefulSetBuilder) PodLabel(key, value string) *StatefulSetBuilder {
    s.labels[key] = value
    return s
}

func (s *StatefulSetBuilder) Container(container corev1.Container) *StatefulSetBuilder {
    s.container = container
    return s
}

func (s *StatefulSetBuilder) Build() *appsv1.StatefulSet {
    return &appsv1.StatefulSet{
        ObjectMeta: metav1.ObjectMeta{
            Name:      s.name,
            Namespace: s.namespace,
        },
        Spec: appsv1.StatefulSetSpec{
            ServiceName: s.serviceName,
            Replicas:    aws.Int32(s.replicas),
            Selector: &metav1.LabelSelector{
                MatchLabels: s.labels,
            },
            Template: corev1.PodTemplateSpec{
                ObjectMeta: metav1.ObjectMeta{
                    Labels: s.labels,
                },
                Spec: corev1.PodSpec{
                    Containers:                    []corev1.Container{s.container},
                    NodeSelector:                  s.nodeSelector,
                    TerminationGracePeriodSeconds: aws.Int64(s.terminationGracePeriod),
                },
            },
        },
    }
}
