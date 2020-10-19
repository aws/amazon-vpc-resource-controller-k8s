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

package handler

import (
	v1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

// Handler interface allows different types of resource implementation to be clubbed under a single handler.
// For instance, warm resource handler would handle all the types of resources that support warm pools. An example
// of warm pool resource is IPv4. Another example of handler is on demand handler, resources that can be only
// processed on demand would fit into this category. For instance, Branch ENIs are tied to the Security
// Group required by the pod which we would know only after receiving the pod request.
type Handler interface {
	CanHandle(resourceName string) bool
	HandleCreate(resourceName string, requestCount int, pod *v1.Pod) (ctrl.Result, error)
	HandleDelete(resourceName string, pod *v1.Pod) (ctrl.Result, error)
}
