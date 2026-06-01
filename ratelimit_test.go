// Copyright (c) 2025 Simon Wilkinson. All rights reserved.
// SPDX-License-Identifier: MIT

package main

import "testing"

func TestBucketKeyIPv4IsFull(t *testing.T) {
	if got := bucketKey("192.0.2.1"); got != "192.0.2.1" {
		t.Errorf("bucketKey(192.0.2.1) = %q, want full /32", got)
	}
	if bucketKey("192.0.2.1") == bucketKey("192.0.2.2") {
		t.Error("distinct IPv4 addresses must map to distinct buckets")
	}
}

func TestBucketKeyIPv6AggregatesToSlash64(t *testing.T) {
	// Two addresses in the same /64 must share a bucket — this is what closes
	// the address-rotation evasion and bounds key growth.
	a := bucketKey("2001:db8:abcd:1234::1")
	b := bucketKey("2001:db8:abcd:1234:ffff:ffff:ffff:ffff")
	if a != b {
		t.Errorf("addresses in the same /64 must share a bucket: %q != %q", a, b)
	}
	// A different /64 must map to a different bucket.
	if c := bucketKey("2001:db8:abcd:9999::1"); c == a {
		t.Errorf("different /64 must not share a bucket: %q == %q", c, a)
	}
}

func TestBucketKeyPassesThroughUnparseable(t *testing.T) {
	if got := bucketKey("not-an-ip"); got != "not-an-ip" {
		t.Errorf("bucketKey(not-an-ip) = %q, want passthrough", got)
	}
}
