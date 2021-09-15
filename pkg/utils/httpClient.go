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

var (
	UserAgentHeader = "User-Agent"
)

// NewRateLimitedClient returns a new HTTP client with rate limiter.
func NewRateLimitedClient(qps int, burst int, userAgent string) (*http.Client, error) {
	if qps == 0 {
		return http.DefaultClient, nil
	}
	if burst < 1 {
		return nil, fmt.Errorf("burst expected >0, got %d", burst)
	}
	return &http.Client{
		Transport: &rateLimitedRoundTripper{
			ua: userAgent,
			rt: http.DefaultTransport,
			rl: rate.NewLimiter(rate.Limit(qps), burst),
		},
	}, nil
}

type rateLimitedRoundTripper struct {
	ua string
	rt http.RoundTripper
	rl *rate.Limiter
}

func (rr *rateLimitedRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rr.SetUserAgent(req)
	if err := rr.rl.Wait(req.Context()); err != nil {
		return nil, err
	}
	return rr.rt.RoundTrip(req)
}

// SetUserAgent add controller name/version in the request's User Agent for tracking
// API calls made by the controller
func (rr *rateLimitedRoundTripper) SetUserAgent(req *http.Request) {
	curUA := req.Header.Get(UserAgentHeader)
	newUA := rr.ua
	if len(curUA) > 0 {
		newUA = newUA + " " + curUA
	}
	req.Header.Set(UserAgentHeader, newUA)
}
