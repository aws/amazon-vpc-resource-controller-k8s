package core

import (
	"context"
	"fmt"
	"net/http"

	v1beta1 "github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1beta1"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/servicequotas"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// SecurityGroupQuotaValidator handles validation of SecurityGroupPolicies
type SecurityGroupQuotaValidator struct {
	Client      *servicequotas.Client
	Decoder     *admission.Decoder
	QuotaCode   string
	ServiceCode string
}

func NewSecurityGroupQuotaValidator() (*SecurityGroupQuotaValidator, error) {
	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		return nil, fmt.Errorf("unable to load SDK config, %w", err)
	}
	svc := servicequotas.NewFromConfig(cfg)
	return &SecurityGroupQuotaValidator{
		Client:      svc,
		QuotaCode:   "L-2AFB9258", // Security groups per network interface QuotaCode
		ServiceCode: "vpc",
	}, nil
}

// Handle allows compare the number of vpc servicequots currently to your AWS account with the number of SecurityGroups requested
func (v *SecurityGroupQuotaValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	policy := &v1beta1.SecurityGroupPolicy{}
	if err := v.Decoder.Decode(req, policy); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	// Get the current quota for security groups
	quota, err := v.Client.GetServiceQuota(ctx, &servicequotas.GetServiceQuotaInput{
		QuotaCode:   &v.QuotaCode,
		ServiceCode: &v.ServiceCode,
	})
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, fmt.Errorf("failed to fetch service quota: %w", err))
	}

	maxGroups := int(*quota.Quota.Value)
	if len(policy.Spec.SecurityGroups.Groups) > maxGroups {
		return admission.Denied(
			fmt.Sprintf("validation error: number of group IDs %d exceeds quota of %d", len(policy.Spec.SecurityGroups.Groups), maxGroups),
		)
	}

	return admission.Allowed("SecurityGroupPolicy validation passed")
}
