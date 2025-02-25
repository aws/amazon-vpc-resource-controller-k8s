package api

func getMockEC2Wrapper() ec2Wrapper {
	return ec2Wrapper{}
}

// Test removed as we've migrated to AWS SDK v2 which handles endpoints differently
