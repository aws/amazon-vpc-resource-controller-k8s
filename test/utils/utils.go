package utils_test

import (
	"context"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
)

func GetAutoScalingGroupName(frameWork *framework.Framework, os string) string {
	By("getting instance details")
	nodeList, err := frameWork.NodeManager.GetNodesWithOS(os)
	Expect(err).ToNot(HaveOccurred())
	Expect(nodeList.Items).ToNot(BeEmpty())
	instanceID := frameWork.NodeManager.GetInstanceID(&nodeList.Items[0])
	Expect(instanceID).ToNot(BeEmpty())
	instance, err := frameWork.EC2Manager.GetInstanceDetails(instanceID)
	Expect(err).ToNot(HaveOccurred())
	tags := utils.GetTagKeyValueMap(instance.Tags)
	val, ok := tags["aws:autoscaling:groupName"]
	Expect(ok).To(BeTrue())
	return val
}

func DeleteNodesWithThrottle(frameWork *framework.Framework, nodeList *v1.NodeList, deletePerMin int) {
	rate := time.Minute / time.Duration(deletePerMin)
	ticker := time.NewTicker(rate)
	defer ticker.Stop()
	for _, node := range nodeList.Items {
		<-ticker.C
		Expect(frameWork.K8sClient.Delete(context.TODO(), &node)).To(Succeed())
	}
}
