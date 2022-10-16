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
	"fmt"
	"net/http"

	"golang.org/x/time/rate"
)

// NewRateLimitedClient returns a new HTTP client with rate limiter.
func NewRateLimitedClient(qps int, burst int) (*http.Client, error) {
	if qps == 0 {
		fmt.Printf("Creating a default rate limited http client with QPS = %d, Burst = %d\n", qps, burst)
		return http.DefaultClient, nil
	}
	if burst < 1 {
		return nil, fmt.Errorf("burst expected >0, got %d", burst)
	}
	return &http.Client{
		Transport: &rateLimitedRoundTripper{
			rt: http.DefaultTransport,
			rl: rate.NewLimiter(rate.Limit(qps), burst),
		},
	}, nil
}

type rateLimitedRoundTripper struct {
	rt http.RoundTripper
	rl *rate.Limiter
}

func (rr *rateLimitedRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := rr.rl.Wait(req.Context()); err != nil {
		return nil, err
	}
	return rr.rt.RoundTrip(req)
}
