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

package healthz

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

type HealthzHandler struct {
	CheckersMap map[string]healthz.Checker
}

var (
	HealthzTimeout time.Duration = 2
)

func NewHealthzHandler(timeout int) *HealthzHandler {
	HealthzTimeout = time.Duration(timeout)
	return &HealthzHandler{
		CheckersMap: make(map[string]healthz.Checker),
	}
}

func (hh *HealthzHandler) AddControllerHealthChecker(name string, controllerCheck healthz.Checker) {
	// only add health check if the map doesn't already contain
	if _, ok := hh.CheckersMap[name]; !ok {
		hh.CheckersMap[name] = controllerCheck
	}
}

func (hh *HealthzHandler) AddControllersHealthCheckers(controllerCheckers map[string]healthz.Checker) {
	for key, value := range controllerCheckers {
		hh.AddControllerHealthChecker(key, value)
	}
}

func (hh *HealthzHandler) AddControllersHealthStatusChecksToManager(mgr manager.Manager) error {
	if len(hh.CheckersMap) > 0 {
		return hh.checkControllersHealthStatus(mgr, hh.CheckersMap)
	} else {
		return errors.New("couldn't find any controller's check to add for healthz endpoint")
	}
}

func (hh *HealthzHandler) checkControllersHealthStatus(mgr manager.Manager, checkers map[string]healthz.Checker) error {
	var err error
	for name, check := range checkers {
		err = mgr.AddHealthzCheck(name, check)
		fmt.Printf("Added Health check for %s with error %v\n", name, err)
		if err != nil {
			break
		}
	}
	return err
}

func SimplePing(controllerName string, log logr.Logger) healthz.Checker {
	log.Info(fmt.Sprintf("%s's healthz subpath was added", controllerName))
	return func(req *http.Request) error {
		log.V(1).Info(fmt.Sprintf("***** %s healthz endpoint was pinged *****", controllerName))
		return nil
	}
}

func PingWithTimeout(healthCheck func(c chan<- error), logger logr.Logger) error {
	status := make(chan error, 1)
	var err error
	go healthCheck(status)

	select {
	case err = <-status:
		logger.V(1).Info("finished healthz check on controller before probing times out", "TimeoutInSecond", HealthzTimeout*time.Second)
	case <-time.After(HealthzTimeout * time.Second):
		err = errors.New("healthz check failed due to timeout")
		logger.Error(err, "healthz check has a preset timeout to fail no responding probing", "TimeoutInSecond", HealthzTimeout*time.Second)
	}
	return err
}
