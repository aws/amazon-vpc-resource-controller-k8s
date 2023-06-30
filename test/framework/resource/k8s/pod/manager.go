// Copyright Amazon.com Inc. or its affiliates. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License"). You may
// not use this file except in compliance with the License. A copy of the
// License is located at
//
//     http://aws.amazon.com/apache2.0/
//
// or in the "license" file accompanying this file. This file is distributed
// on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either
// express or implied. See the License for the specific language governing
// permissions and limitations under the License.

package pod

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider/branch/trunk"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

type Manager interface {
	CreateAndWaitTillPodIsRunning(context context.Context, pod *v1.Pod, timeOut time.Duration) (*v1.Pod, error)
	CreateAndWaitTillPodIsCompleted(context context.Context, pod *v1.Pod) (*v1.Pod, error)
	DeleteAndWaitTillPodIsDeleted(context context.Context, pod *v1.Pod) error
	DeleteAllPodsForcefully(context context.Context, podLabelKey string, podLabelVal string) error
	GetENIDetailsFromPodAnnotation(podAnnotation map[string]string) ([]*trunk.ENIDetails, error)
	GetPodsWithLabel(context context.Context, namespace string, labelKey string, labelValue string) ([]v1.Pod, error)
	PatchPod(context context.Context, oldPod *v1.Pod, newPod *v1.Pod) error
	PodExec(namespace string, name string, command []string) (string, string, error)
}

type defaultManager struct {
	k8sClient client.Client
	k8sSchema *runtime.Scheme
	config    *rest.Config
}

func NewManager(k8sClient client.Client, k8sSchema *runtime.Scheme,
	config *rest.Config) Manager {
	return &defaultManager{
		k8sClient: k8sClient,
		k8sSchema: k8sSchema,
		config:    config,
	}
}

func (d *defaultManager) CreateAndWaitTillPodIsRunning(context context.Context, pod *v1.Pod, timeOut time.Duration) (*v1.Pod, error) {
	err := d.k8sClient.Create(context, pod)
	if err != nil {
		return nil, err
	}

	updatedPod := &v1.Pod{}
	err = wait.Poll(utils.PollIntervalShort, timeOut, func() (done bool, err error) {
		err = d.k8sClient.Get(context, utils.NamespacedName(pod), updatedPod)
		if err != nil {
			return true, err
		}
		return isPodReady(updatedPod), nil
	})

	return updatedPod, err
}

func (d *defaultManager) CreateAndWaitTillPodIsCompleted(context context.Context, pod *v1.Pod) (*v1.Pod, error) {
	err := d.k8sClient.Create(context, pod)
	if err != nil {
		return nil, err
	}

	updatedPod := &v1.Pod{}
	err = wait.PollUntil(utils.PollIntervalShort, func() (done bool, err error) {
		err = d.k8sClient.Get(context, utils.NamespacedName(pod), updatedPod)
		if err != nil {
			return true, err
		}
		if isPodCompleted(updatedPod) {
			return true, nil
		}
		if isPodFailed(updatedPod) {
			return true, fmt.Errorf("pod failed to start")
		}
		return false, nil
	}, context.Done())

	return updatedPod, err
}

func (d *defaultManager) GetPodsWithLabel(context context.Context, namespace string,
	labelKey string, labelValue string) ([]v1.Pod, error) {

	podList := &v1.PodList{}
	err := d.k8sClient.List(context, podList, &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(labels.Set{labelKey: labelValue}),
		Namespace:     namespace,
	})

	return podList.Items, err
}

func (d *defaultManager) DeleteAndWaitTillPodIsDeleted(context context.Context, pod *v1.Pod) error {
	if err := d.k8sClient.Delete(context, pod); err != nil {
		return client.IgnoreNotFound(err)
	}

	observedPod := &v1.Pod{}
	return wait.PollUntil(utils.PollIntervalShort, func() (done bool, err error) {
		err = d.k8sClient.Get(context, client.ObjectKeyFromObject(pod), observedPod)
		if errors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}, context.Done())
}

func (d *defaultManager) DeleteAllPodsForcefully(context context.Context,
	podLabelKey string, podLabelVal string) error {

	podList := &v1.PodList{}
	d.k8sClient.List(context, podList, &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(labels.Set{podLabelKey: podLabelVal}),
	})

	if len(podList.Items) == 0 {
		return fmt.Errorf("no pods found with label %s:%s", podLabelKey, podLabelVal)
	}

	gracePeriod := int64(0)
	for _, pod := range podList.Items {
		err := d.k8sClient.Delete(context, &pod, &client.DeleteOptions{
			GracePeriodSeconds: &gracePeriod,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (d *defaultManager) GetENIDetailsFromPodAnnotation(podAnnotation map[string]string) ([]*trunk.ENIDetails, error) {
	branchDetails, hasAnnotation := podAnnotation[config.ResourceNamePodENI]
	if !hasAnnotation {
		return nil, fmt.Errorf("failed to find annotation on pod %v", podAnnotation)
	}
	eniDetails := []*trunk.ENIDetails{}
	json.Unmarshal([]byte(branchDetails), &eniDetails)

	return eniDetails, nil
}

func (d *defaultManager) PatchPod(context context.Context, oldPod *v1.Pod, newPod *v1.Pod) error {
	return d.k8sClient.Patch(context, newPod, client.MergeFrom(oldPod))
}

func isPodReady(pod *v1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Status == v1.ConditionTrue && condition.Type == v1.PodReady {
			return true
		}
	}
	return false
}

func isPodCompleted(pod *v1.Pod) bool {
	return pod.Status.Phase == v1.PodSucceeded
}

func isPodFailed(pod *v1.Pod) bool {
	return pod.Status.Phase == v1.PodFailed
}

func (d *defaultManager) PodExec(namespace string, name string, command []string) (string, string, error) {
	restClient, err := d.getRestClientForPod(namespace, name)
	if err != nil {
		return "", "", err
	}

	execOptions := &v1.PodExecOptions{
		Stdout:  true,
		Stderr:  true,
		Command: command,
	}

	restClient.Get()
	req := restClient.Post().
		Resource("pods").
		Name(name).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(execOptions, runtime.NewParameterCodec(d.k8sSchema))

	exec, err := remotecommand.NewSPDYExecutor(d.config, http.MethodPost, req.URL())
	if err != nil {
		return "", "", err
	}

	var stdout, stderr bytes.Buffer
	err = exec.Stream(remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})

	return stdout.String(), stderr.String(), err
}

func (d *defaultManager) getRestClientForPod(namespace string, name string) (rest.Interface, error) {
	pod := &v1.Pod{}

	err := d.k8sClient.Get(context.Background(), types.NamespacedName{
		Namespace: namespace,
		Name:      name,
	}, pod)
	if err != nil {
		return nil, err
	}

	gkv, err := apiutil.GVKForObject(pod, d.k8sSchema)
	if err != nil {
		return nil, err
	}
	return apiutil.RESTClientForGVK(gkv, false, d.config, serializer.NewCodecFactory(d.k8sSchema))
}
