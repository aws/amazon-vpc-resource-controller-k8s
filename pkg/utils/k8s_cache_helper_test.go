package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	vpcresourcesv1beta1 "github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1beta1"
)

// TestK8sCacheHelper_GetSecurityGroupsFromPod tests the API to get Security Group from k8s cache.
func TestK8sCacheHelper_GetSecurityGroupsFromPod(t *testing.T) {
	podList := &corev1.PodList{}
	saList := &corev1.ServiceAccountList{}
	sgpList := &vpcresourcesv1beta1.SecurityGroupPolicyList{}
	testClient.List(nil, podList)
	assert.True(t, len(podList.Items) == 3)

	testClient.List(nil, saList)
	assert.True(t, len(saList.Items) == 1)

	testClient.List(nil, sgpList)
	assert.True(t, len(sgpList.Items) == 2)

	sgList, _ := helper.GetSecurityGroupsFromPod(types.NamespacedName{
		Name:      testPod.Name,
		Namespace: testPod.Namespace,
	})
	assert.True(t, len(sgList) == len(testSecurityGroupsOne))
	assert.True(t, isEverySecurityGroupIncluded(sgList))
}

// TestK8sCacheHelper_GetNoSecurityGroupsFromPod tests the API to get zero Security Group from k8s cache.
func TestK8sCacheHelper_GetNoSecurityGroupsFromPod(t *testing.T) {
	sgList, _ := helper.GetSecurityGroupsFromPod(types.NamespacedName{
		Name:      testPod.Name + "_NoENI",
		Namespace: testPod.Namespace,
	})
	assert.True(t, len(sgList) == 0)
}

// TestK8sCacheHelper_GetMultipleSecurityGroupsFromPod tests the API to get Security Groups from more than one SGP in cache.
func TestK8sCacheHelper_GetMultipleSecurityGroupsFromPod(t *testing.T) {
	sgList, _ := helper.GetSecurityGroupsFromPod(types.NamespacedName{
		Name:      testPod.Name + "_ENIs",
		Namespace: testPod.Namespace,
	})
	assert.True(t, len(sgList) == len(append(testSecurityGroupsOne, testSecurityGroupsTwo...)))
}
