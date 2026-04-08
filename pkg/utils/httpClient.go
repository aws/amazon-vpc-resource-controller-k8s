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

package utils

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/time/rate"
)

const (
	// defaultAWSSDKClientTimeout is the timeout for individual HTTP requests made by AWS SDK clients.
	defaultAWSSDKClientTimeout = 30 * time.Second
)

// NewAWSSDKHTTPClient returns a new HTTP client with the default AWS SDK timeout.
func NewAWSSDKHTTPClient() *http.Client {
	return &http.Client{Timeout: defaultAWSSDKClientTimeout}
}

// NewRateLimitedClient returns a new HTTP client with rate limiter and the default AWS SDK timeout.
// The timeout is applied after the rate limit wait, so queue time does not eat into the HTTP timeout.
func NewRateLimitedClient(qps int, burst int) (*http.Client, error) {
	if qps == 0 {
		return NewAWSSDKHTTPClient(), nil
	}
	if burst < 1 {
		return nil, fmt.Errorf("burst expected >0, got %d", burst)
	}
	return &http.Client{
		Transport: &rateLimitedRoundTripper{
			rt:      http.DefaultTransport,
			rl:      rate.NewLimiter(rate.Limit(qps), burst),
			timeout: defaultAWSSDKClientTimeout,
		},
	}, nil
}

type rateLimitedRoundTripper struct {
	rt      http.RoundTripper
	rl      *rate.Limiter
	timeout time.Duration
}

func (rr *rateLimitedRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := rr.rl.Wait(req.Context()); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(req.Context(), rr.timeout)
	defer cancel()
	return rr.rt.RoundTrip(req.WithContext(ctx))
}
