# VPC Limits Generator

This tool generates the `pkg/aws/vpc/limits.go` file by querying the AWS EC2 API for instance type information including ENI and IP address limits.

## Prerequisites

1. **AWS Credentials**: Ensure you have valid AWS credentials configured (via `~/.aws/credentials` or environment variables)
2. **AWS Permissions**: You need `ec2:DescribeInstanceTypes` permission
3. **Go**: Go 1.24 or higher (as specified in go.mod)

## Usage

### Using Make (Recommended)

```bash
make generate-vpc-limits
```

### Running Directly

```bash
go run hack/vpc-limits-gen/main.go --output pkg/aws/vpc/limits.go --region us-east-1
```

### Options

- `--output`: Output file path (default: `pkg/aws/vpc/limits.go`)
- `--region`: AWS region to query (default: `us-east-1`)

## How It Works

1. **Queries AWS EC2 API**: Uses `DescribeInstanceTypes` to fetch all available instance types and their network configurations
2. **Calculates Branch ENIs**: For Nitro-based instances (hypervisor="nitro" or bare metal), calculates the maximum branch ENIs using the formula: `(MaxENIs - 1) × (MaxIPv4PerENI - 1)`
   - One ENI is reserved as the trunk, leaving `(MaxENIs - 1)` available
   - Each available ENI can create branch ENIs using its secondary IPs: `(MaxIPv4PerENI - 1)` per ENI
   - Total branch ENI capacity = available ENIs × secondary IPs per ENI
3. **Calculates Max Pods**: Implements EKS AMI methodology for calculating pod capacity:
   - **Secondary IP Mode**: `ENIs × (IPs_per_ENI - 1) + 2`
   - **Prefix Delegation Mode**: `ENIs × ((IPs_per_ENI - 1) × 16) + 2` (Nitro only)
   - **Recommended Limit**: Applies CPU-based ceiling (110 for ≤30 vCPUs, 250 for >30 vCPUs)
4. **Generates Go Code**: Creates properly formatted Go code with all instance types sorted alphabetically
5. **Maintains Compatibility**: The generated file is compatible with Karpenter, which fetches this file weekly via their codegen workflow

## Generated File Format

The generated file includes comprehensive networking and capacity information for each instance type:

### Network Configuration Fields

- **Instance Type Name**: The AWS instance type identifier (e.g., "m5.4xlarge")
- **Interface**: Maximum number of network interfaces (ENIs) the instance can support
- **IPv4PerInterface**: Maximum IPv4 addresses per interface
- **IsTrunkingCompatible**: Boolean indicating if the instance supports ENI trunking (Nitro instances only)
- **BranchInterface**: Maximum branch ENIs for Security Groups for Pods feature
  - Formula: `(MaxENIs - 1) × (MaxIPv4PerENI - 1)`
  - Example: m5.4xlarge with 8 ENIs and 30 IPs → `(8-1) × (30-1) = 203` branch ENIs
- **NetworkCards**: Array of network card information for multi-card instances
- **DefaultNetworkCardIndex**: Index of the default network card (usually 0)
- **Hypervisor**: Hypervisor type ("nitro", "xen", or "" for bare metal)
- **IsBareMetal**: Boolean indicating if it's a bare metal instance

### Pod Capacity Fields (New)

These fields help users make informed decisions about instance sizing and networking configuration:

- **MaxPodsSecondaryIPs**: Maximum pods when using secondary IP mode (default)
  - Formula: `ENIs × (IPs_per_ENI - 1) + 2`
  - Example: m5.4xlarge → `8 × (30-1) + 2 = 234` pods
  - **Benefit**: Know actual capacity for default EKS configuration

- **MaxPodsPrefixDelegation**: Maximum pods when prefix delegation is enabled
  - Formula: `ENIs × ((IPs_per_ENI - 1) × 16) + 2` (16 IPs per prefix)
  - Example: m5.4xlarge → `8 × ((30-1) × 16) + 2 = 3,714` pods
  - **Benefit**: Understand dramatic capacity increase (15.9× in this example) for high-density workloads
  - **Note**: For non-Nitro instances, this equals MaxPodsSecondaryIPs (they can't use prefix delegation)

- **MaxPodsRecommended**: AWS recommended pod limit accounting for CPU constraints
  - Applies ceiling based on vCPU count: 110 (≤30 vCPUs) or 250 (>30 vCPUs)
  - Example: m5.4xlarge with 16 vCPUs → min(3714, 110) = 110 pods
  - **Benefit**: Ensures stable, performant clusters by preventing kubelet overload

- **CPUCount**: Number of vCPUs for the instance
  - **Benefit**: Helps users understand why MaxPodsRecommended differs from theoretical limits

### Example Entry

```go
"m5.4xlarge": {
    Interface:               8,
    IPv4PerInterface:        30,
    IsTrunkingCompatible:    true,
    BranchInterface:         203,  // (8-1) × (30-1) = 203
    DefaultNetworkCardIndex: 0,
    NetworkCards: []NetworkCard{
        {
            MaximumNetworkInterfaces: 8,
            NetworkCardIndex:         0,
        },
    },
    Hypervisor:              "nitro",
    IsBareMetal:             false,
    MaxPodsSecondaryIPs:     234,   // 8 × (30-1) + 2 = 234
    MaxPodsPrefixDelegation: 3714,  // 8 × ((30-1) × 16) + 2 = 3,714
    MaxPodsRecommended:      110,   // min(3714, 110) due to 16 vCPUs
    CPUCount:                16,
},
```

## Benefits for EKS Users

The generated limits file provides critical data for:

1. **Capacity Planning**: Understand pod density limits before deploying
2. **Cost Optimization**: Choose right-sized instances based on actual pod capacity
3. **Performance Tuning**: Respect recommended limits to avoid kubelet/node issues
4. **Configuration Decisions**: Evaluate whether prefix delegation is worth enabling
5. **Troubleshooting**: Quickly identify if pod scheduling issues are IP-related
6. **Automated Scaling**: Tools like Karpenter use this data for intelligent instance selection

## Updating the Limits File

To include the latest AWS instance types:

1. Run `make generate-vpc-limits`
2. Review the generated changes with `git diff pkg/aws/vpc/limits.go`
3. Commit the changes

**Note**: Karpenter's automated codegen workflow will pick up changes from this repository's master branch, so updates here will automatically propagate to Karpenter.

## Relationship with Karpenter

Karpenter's `codegen` workflow (https://github.com/aws/karpenter-provider-aws/blob/main/hack/codegen.sh) fetches this file from the master branch of this repository:

```bash
go run hack/code/vpc_limits_gen/main.go -- \
  --url=https://raw.githubusercontent.com/aws/amazon-vpc-resource-controller-k8s/master/pkg/aws/vpc/limits.go \
  --output="pkg/providers/instancetype/zz_generated.vpclimits.go"
```

This ensures both repositories stay in sync with the latest instance type information.

## Troubleshooting

### Missing AWS Credentials

If you see an error like "NoCredentialProviders: no valid providers in chain":

```bash
# Configure AWS credentials
aws configure
# OR set environment variables
export AWS_ACCESS_KEY_ID=your_access_key
export AWS_SECRET_ACCESS_KEY=your_secret_key
export AWS_REGION=us-east-1
```

### Permission Denied

If you get "UnauthorizedOperation" or similar errors, ensure your IAM user/role has the `ec2:DescribeInstanceTypes` permission.

### Instance Type Missing

If a specific instance type is missing from the generated file:

1. Verify the instance type exists in the AWS region you're querying (some types are region-specific)
2. Try querying a different region with `--region us-west-2` or another region where the instance type is available
3. Check AWS documentation to confirm the instance type is generally available
