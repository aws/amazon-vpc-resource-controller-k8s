/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package handler

import (
	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
)

type warmResourceHandler struct {
	Log logr.Logger
}

func (w *warmResourceHandler) CanHandle(resourceName string) bool {

	// TODO: CanHandle logic for warm resources.
	return false
}

func (w *warmResourceHandler) HandleCreate(resourceName string, requestCount int64, pod *v1.Pod) error {

	// TODO: Create handling logic for warm resources.
	return nil
}

func (w *warmResourceHandler) HandleDelete(resourceName string, pod *v1.Pod) error {

	// TODO: Delete handling logic for warm resources.
	return nil
}
