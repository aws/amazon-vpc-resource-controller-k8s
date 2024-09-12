package api

import (
	"testing"
)

func getMockEC2Wrapper() ec2Wrapper {
	return ec2Wrapper{}
}
func Test_getRegionalStsEndpoint(t *testing.T) {

	ec2Wapper := getMockEC2Wrapper()

	type args struct {
		partitionID string
		region      string
	}

	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "service doesn't exist in partition",
			args: args{
				partitionID: "aws-iso-f",
				region:      "testregions",
			},
			want:    "https://sts.testregions.csp.hci.ic.gov",
			wantErr: false,
		},
		{
			name: "region doesn't exist in partition",
			args: args{
				partitionID: "aws",
				region:      "us-test-2",
			},
			want:    "https://sts.us-test-2.amazonaws.com",
			wantErr: false,
		},
		{
			name: "region and service exist in partition",
			args: args{
				partitionID: "aws",
				region:      "us-west-2",
			},
			want:    "https://sts.us-west-2.amazonaws.com",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ec2Wapper.getRegionalStsEndpoint(tt.args.partitionID, tt.args.region)
			if (err != nil) != tt.wantErr {
				t.Errorf("getRegionalStsEndpoint() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got.URL != tt.want {
				t.Errorf("getRegionalStsEndpoint() = %v, want %v", got, tt.want)
			}
		})
	}
}
