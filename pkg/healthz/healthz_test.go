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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var testTimeout = 3

// TestHealthzHandler tests creating a new healthz handler with timeout value passed to it
func TestHealthzHandler(t *testing.T) {
	handler := NewHealthzHandler(testTimeout)
	assert.True(t, handler != nil)
	assert.True(t, HealthzTimeout == time.Duration(testTimeout))
}

// TestAddControllerHealthChecker tests adding individual healthz checker
func TestAddControllerHealthChecker(t *testing.T) {
	handler := NewHealthzHandler(testTimeout)
	checker := healthz.Ping
	name := "test-ping"
	handler.AddControllerHealthChecker(name, checker)
	assert.True(t, len(handler.CheckersMap) == 1, "Should be only one healthz checker")
	_, ok := handler.CheckersMap[name]
	assert.True(t, ok)
}

// TestAddControllersHealthCheckers tests adding the map of healthz checkers
func TestAddControllersHealthCheckers(t *testing.T) {
	handler := NewHealthzHandler(testTimeout)
	checkers := map[string]healthz.Checker{
		"test-checker-1": healthz.Ping,
		"test-checker-2": SimplePing("test", zap.New()),
	}
	handler.AddControllersHealthCheckers(checkers)
	assert.True(t, len(handler.CheckersMap) == 2, "Two checkers should be added")
}

// TestPingWithTimeout_Success tests ping responding before timeout
func TestPingWithTimeout_Success(t *testing.T) {
	err := PingWithTimeout(func(c chan<- error) {
		time.Sleep(1 * time.Second)
		c <- nil
	}, zap.New())
	time.Sleep(5 * time.Second)
	assert.NoError(t, err)
}

// TestPingWithTimeout_Failure tests ping responding after timeout
func TestPingWithTimeout_Failure(t *testing.T) {
	err := PingWithTimeout(func(c chan<- error) {
		time.Sleep(4 * time.Second)
		c <- nil
	}, zap.New())
	time.Sleep(5 * time.Second)
	assert.Error(t, err)
	assert.EqualErrorf(t, err, "healthz check failed due to timeout", "Healthz check should fail due to timeout")
}
