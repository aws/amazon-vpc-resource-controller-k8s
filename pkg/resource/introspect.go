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

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	GetNodeResourcesPath = "/node/"
	GetAllResourcesPath  = "/resources"
)

type IntrospectHandler struct {
	Log             logr.Logger
	BindAddress     string
	ResourceManager ResourceManager
}

// StartENICleaner starts the ENI Cleaner routine that cleans up dangling ENIs created by the controller
func (i *IntrospectHandler) Start(_ context.Context) error {
	i.Log.Info("starting introspection API")

	mux := http.NewServeMux()
	mux.HandleFunc(GetAllResourcesPath, i.ResourceHandler)
	mux.HandleFunc(GetNodeResourcesPath, i.NodeResourceHandler)

	// Should this be a fatal error?
	err := http.ListenAndServe(i.BindAddress, mux)
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

func (i *IntrospectHandler) SetupWithManager(mgr ctrl.Manager) error {
	return mgr.Add(i)
}
