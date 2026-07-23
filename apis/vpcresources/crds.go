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

// Package vpcresources exposes the vpcresources.k8s.aws CustomResourceDefinitions
// as Go objects so downstream components can install or upgrade them
// programmatically (e.g. an operator ensuring the cluster's installed CRD is at
// least the schema version this module was built against).
//
// The embedded YAML files are copies of config/crd/bases (the controller-gen
// output); `make verify` refreshes them and CI fails if they drift.
package vpcresources

import (
	_ "embed"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

//go:embed crds/vpcresources.k8s.aws_cninodes.yaml
var CNINodeCRDBytes []byte

//go:embed crds/vpcresources.k8s.aws_securitygrouppolicies.yaml
var SecurityGroupPolicyCRDBytes []byte

// CRDs is the list of CustomResourceDefinitions of the vpcresources.k8s.aws
// group, unmarshalled from the embedded controller-gen output.
var CRDs = []*apiextensionsv1.CustomResourceDefinition{
	mustUnmarshalCRD(CNINodeCRDBytes),
	mustUnmarshalCRD(SecurityGroupPolicyCRDBytes),
}

func mustUnmarshalCRD(data []byte) *apiextensionsv1.CustomResourceDefinition {
	crd := &apiextensionsv1.CustomResourceDefinition{}
	if err := yaml.Unmarshal(data, crd); err != nil {
		panic("vpcresources: embedded CRD yaml is invalid: " + err.Error())
	}
	return crd
}
