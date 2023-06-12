package utils

import "errors"

var (
	ErrNotFound               = errors.New("resource was not found")
	ErrInsufficientCidrBlocks = errors.New("InsufficientCidrBlocks: The specified subnet does not have enough free cidr blocks to satisfy the request")
)
