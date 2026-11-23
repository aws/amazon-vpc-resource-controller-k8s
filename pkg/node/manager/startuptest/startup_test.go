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

// Package startuptest is its own test binary because the manager package's fixtures build a managed node at init,
// which registers the restore series and would hide whether NewNodeManager registers them.
package startuptest

import (
	"testing"

	mock_worker "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/worker"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/healthz"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/node/manager"

	"github.com/golang/mock/gomock"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

func restoreSeries() ([]*dto.Metric, error) {
	families, err := metrics.Registry.Gather()
	for _, family := range families {
		if family.GetName() == "cninode_checkpoint_restore_total" {
			return family.GetMetric(), err
		}
	}
	return nil, err
}

// Read at init because the registry is process-wide: a repeat under -count starts with the series already exposed.
var seriesBeforeManager, errBeforeManager = restoreSeries()

// A standby builds the node manager but no managed node before it takes over, so the manager itself must expose the
// restore series at 0 for the metrics agent to read before the restores.
func TestNewNodeManager_ExposesRestoreSeriesAtZero(t *testing.T) {
	assert.NoError(t, errBeforeManager)
	assert.Empty(t, seriesBeforeManager, "restore series exist before the manager is built, so this test cannot see its call")

	ctrl := gomock.NewController(t)
	worker := mock_worker.NewMockWorker(ctrl)
	worker.EXPECT().StartWorkerPool(gomock.Any()).Return(nil)
	_, err := manager.NewNodeManager(zap.New(), nil, api.Wrapper{}, worker, nil, "cluster-name", "v0.0.0",
		healthz.NewHealthzHandler(5))
	assert.NoError(t, err)

	series, err := restoreSeries()
	assert.NoError(t, err)
	assert.NotEmpty(t, series, "cninode_checkpoint_restore_total is not exposed after NewNodeManager")
	for _, s := range series {
		assert.Zero(t, s.GetCounter().GetValue())
	}
}
