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

//go:build deadlock

package manager

import deadlock "github.com/sasha-s/go-deadlock"

// rwMutex swaps in go-deadlock's instrumented RWMutex when built with
// `-tags deadlock`. It reports lock-order inversions (a classic two-lock
// deadlock signature -- e.g. the manager lock and a node lock acquired in
// opposite orders on different goroutines) and locks held longer than
// deadlock.Opts.DeadlockTimeout. Production builds use the plain
// sync.RWMutex alias in rwmutex.go instead, so there is no runtime cost
// unless this tag is set (test-only).
type rwMutex = deadlock.RWMutex
