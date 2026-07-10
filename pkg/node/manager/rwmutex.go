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

//go:build !deadlock

package manager

import "sync"

// rwMutex is the node manager's lock type. In normal builds it is the standard
// library sync.RWMutex (zero extra cost). Build with `-tags deadlock` to swap in
// sasha-s/go-deadlock's instrumented RWMutex, which detects lock-order inversions
// and long lock holds at runtime -- see rwmutex_deadlock.go. Used by the manager's
// concurrency tests via `go test -tags deadlock -race ./pkg/node/manager/...`.
type rwMutex = sync.RWMutex
