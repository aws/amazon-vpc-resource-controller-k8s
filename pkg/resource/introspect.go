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

package resource

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	rcHealthz "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/healthz"
	podWrapper "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/k8s/pod"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
)

const (
	GetNodeResourcesPath    = "/node/"
	GetAllResourcesPath     = "/resources/all"
	GetResourcesSummaryPath = "/resources/summary"
	// Use path "/datastore/all" to get all pods stored in the cache,
	// "/datastore/listpods/<nodename>" to get list of all pods on a node (ie ListPods output)
	// "datastore/listrunningpods/<nodename>" to get list of all running pods on a node (ie GetRunningPodsOnNode output)
	GetDatastoreResourcePrefix = "/datastore/"
	InvalidDSRequestMessage    = "Invalid request, valid requests for datastore are /datastore/all, /datastore/listpods/<nodename>, datastore/listrunningpods/<nodename>"
)

type IntrospectHandler struct {
	Log             logr.Logger
	BindAddress     string
	ResourceManager ResourceManager
	PodAPIWrapper   podWrapper.PodClientAPIWrapper
}

// Start the introspection API
func (i *IntrospectHandler) Start(_ context.Context) error {
	i.Log.Info("starting introspection API")

	mux := http.NewServeMux()
	mux.HandleFunc(GetAllResourcesPath, i.ResourceHandler)
	mux.HandleFunc(GetNodeResourcesPath, i.NodeResourceHandler)
	mux.HandleFunc(GetResourcesSummaryPath, i.ResourceSummaryHandler)
	mux.HandleFunc(GetDatastoreResourcePrefix, i.DatastoreResourceHandler)

	// Should this be a fatal error?
	err := http.ListenAndServe(i.BindAddress, mux) // #nosec G114
	if err != nil {
		i.Log.Error(err, "failed to run introspect API")
	}
	return err
}

// ResourceHandler returns all the nodes associated with the resource
func (i *IntrospectHandler) ResourceHandler(w http.ResponseWriter, _ *http.Request) {
	response := make(map[string]interface{})
	for resourceName, provider := range i.ResourceManager.GetResourceProviders() {
		data := provider.Introspect()
		response[resourceName] = data
	}

	jsonData, err := json.MarshalIndent(response, "", "\t")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		return
	}

	w.Header().Set("content-type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(jsonData)
}

// NodeResourceHandler returns all the resources associated with the Node
func (i *IntrospectHandler) NodeResourceHandler(w http.ResponseWriter, r *http.Request) {
	nodeName := r.URL.Path[len(GetNodeResourcesPath):]

	response := make(map[string]interface{})
	for resourceName, provider := range i.ResourceManager.GetResourceProviders() {
		data := provider.IntrospectNode(nodeName)
		if data != nil {
			response[resourceName] = data
		}
	}

	jsonData, err := json.MarshalIndent(response, "", "\t")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		return
	}

	w.Header().Set("content-type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(jsonData)
}

// ResourceSummaryHandler returns all the resources associated with the Node
func (i *IntrospectHandler) ResourceSummaryHandler(w http.ResponseWriter, r *http.Request) {
	response := make(map[string]interface{})
	for resourceName, provider := range i.ResourceManager.GetResourceProviders() {
		data := provider.IntrospectSummary()
		response[resourceName] = data
	}

	jsonData, err := json.MarshalIndent(response, "", "\t")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		return
	}

	w.Header().Set("content-type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(jsonData)
}

func (i *IntrospectHandler) DatastoreResourceHandler(w http.ResponseWriter, r *http.Request) {
	response := make(map[string]interface{})

	request := r.URL.Path[len(GetDatastoreResourcePrefix):]
	request, nodeName, found := strings.Cut(request, "/")
	if !found {
		if request != "all" && !slices.Contains([]string{"listpods", "listrunningpods"}, request) {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(InvalidDSRequestMessage))
			return
		}
	}
	var err error
	var isError bool
	switch request {
	case "listpods":
		var podList *v1.PodList
		if podList, err = i.PodAPIWrapper.ListPods(nodeName); err != nil {
			isError = true
			break
		}
		response[nodeName] = getPodNameSpaceName(podList.Items)
	case "listrunningpods":
		var pods []v1.Pod
		if pods, err = i.PodAPIWrapper.GetRunningPodsOnNode(nodeName); err != nil {
			isError = true
			break
		}
		response[nodeName] = getPodNameSpaceName(pods)
	case "all":
		response["PodDatastore"] = i.PodAPIWrapper.Introspect()
	default:
		// will not execute, adding it for future cases
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(InvalidDSRequestMessage))
		return
	}
	if isError {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(err.Error()))
		return
	}

	jsonData, err := json.MarshalIndent(response, "", "\t")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		return
	}
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(jsonData)
}

func (i *IntrospectHandler) SetupWithManager(mgr ctrl.Manager, healthzHanlder *rcHealthz.HealthzHandler) error {
	// add health check on subpath for introspect controller
	healthzHanlder.AddControllersHealthCheckers(
		map[string]healthz.Checker{"health-introspect-controller": rcHealthz.SimplePing("Introspect controller", i.Log)},
	)

	return mgr.Add(i)
}

func getPodNameSpaceName(podList []v1.Pod) interface{} {
	var podNameSpaceName []string
	for _, pod := range podList {
		podNameSpaceName = append(podNameSpaceName, types.NamespacedName{Namespace: pod.Namespace, Name: pod.Name}.String())
	}
	return podWrapper.IntrospectResponse{
		PodList: podNameSpaceName,
	}
}
