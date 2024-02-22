// Copyright 2019-2022 The NATS Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cli

import (
	"errors"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/tiiuae/jsm.go/api"
)

func checkErr(t *testing.T, err error, format string, a ...any) {
	t.Helper()
	if err == nil {
		return
	}

	t.Fatalf(format, a...)
}

func assertNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
}

func assertListIsEmpty(t *testing.T, list []string) {
	t.Helper()

	if len(list) > 0 {
		t.Fatalf("invalid items: %v", list)
	}
}

func assertListEquals(t *testing.T, list []string, vals ...string) {
	t.Helper()

	sort.Strings(list)
	sort.Strings(vals)

	if !cmp.Equal(list, vals) {
		t.Fatalf("invalid items: %v", list)
	}
}

func TestParseStringAsBytes(t *testing.T) {
	cases := []struct {
		input  string
		expect int64
		error  bool
	}{
		{input: "1", expect: 1},
		{input: "1000", expect: 1000},
		{input: "1K", expect: 1024},
		{input: "1k", expect: 1024},
		{input: "1KB", expect: 1024},
		{input: "1KiB", expect: 1024},
		{input: "1kb", expect: 1024},
		{input: "1M", expect: 1024 * 1024},
		{input: "1MB", expect: 1024 * 1024},
		{input: "1MiB", expect: 1024 * 1024},
		{input: "1m", expect: 1024 * 1024},
		{input: "1G", expect: 1024 * 1024 * 1024},
		{input: "1GB", expect: 1024 * 1024 * 1024},
		{input: "1GiB", expect: 1024 * 1024 * 1024},
		{input: "1g", expect: 1024 * 1024 * 1024},
		{input: "1T", expect: 1024 * 1024 * 1024 * 1024},
		{input: "1TB", expect: 1024 * 1024 * 1024 * 1024},
		{input: "1TiB", expect: 1024 * 1024 * 1024 * 1024},
		{input: "1t", expect: 1024 * 1024 * 1024 * 1024},
		{input: "-1", expect: -1},
		{input: "-10", expect: -1},
		{input: "-10GB", expect: -1},
		{input: "1B", error: true},
		{input: "1FOO", error: true},
		{input: "FOO", error: true},
	}

	for _, c := range cases {
		v, err := parseStringAsBytes(c.input)
		if c.error {
			if !errors.Is(err, errInvalidByteString) {
				t.Fatalf("expected an invalid bytes error got: %v", err)
			}
		} else {
			if err != nil {
				t.Fatalf("did not expect an error parsing %v: %v", c.input, err)
			}
			if v != c.expect {
				t.Fatalf("expected %v to parse as %d got %d", c.input, c.expect, v)
			}
		}
	}
}

func TestSplitString(t *testing.T) {
	for _, s := range []string{"x y", "x	y", "x  y", "x,y", "x, y"} {
		parts := splitString(s)
		if parts[0] != "x" && parts[1] != "y" {
			t.Fatalf("Expected x and y from %s, got %v", s, parts)
		}
	}

	parts := splitString("x foo.*")
	if parts[0] != "x" && parts[1] != "y" {
		t.Fatalf("Expected x and foo.* from 'x foo.*', got %v", parts)
	}
}

func TestParseDurationString(t *testing.T) {
	d, err := parseDurationString("")
	checkErr(t, err, "failed to parse empty duration: %s", err)
	if d.Nanoseconds() != 0 {
		t.Fatalf("expected 0 ns from empty duration, got %v", d)
	}

	_, err = parseDurationString("1f")
	if err.Error() != "invalid duration: invalid unit f" {
		t.Fatal("expected time unit 'f' to fail but it did not")
	}

	for _, u := range []string{"d", "D"} {
		d, err = parseDurationString("1.1" + u)
		checkErr(t, err, "failed to parse 1.1%s duration: %s", u, err)
		if d.Hours() != 26 {
			t.Fatalf("expected 1 hour from 1.1%s duration, got %v", u, d)
		}
	}

	d, err = parseDurationString("1.1M")
	checkErr(t, err, "failed to parse 1.1M duration: %s", err)
	if d.Hours() != 1.1*24*30 {
		t.Fatalf("expected 30 days from 1.1M duration, got %v", d)
	}

	for _, u := range []string{"y", "Y"} {
		d, err = parseDurationString("1.1" + u)
		checkErr(t, err, "failed to parse 1.1%s duration: %s", u, err)
		if d.Hours() != 1.1*24*365 {
			t.Fatalf("expected 1.1 year from 1.1%s duration, got %v", u, d)
		}
	}

	d, err = parseDurationString("1.1h")
	checkErr(t, err, "failed to parse 1.1h duration: %s", err)
	if d.Minutes() != 66 {
		t.Fatalf("expected 1.1 hour from 1.1h duration, got %v", d)
	}
}

func TestRandomString(t *testing.T) {
	for i := 0; i < 1000; i++ {
		if len(randomString(1024, 1024)) != 1024 {
			t.Fatalf("got a !1024 length string")
		}
	}

	for i := 0; i < 1000; i++ {
		n := randomString(2024, 1024)
		if len(n) > 2024 {
			t.Fatalf("got a > 2024 length string")
		}

		if len(n) < 1024 {
			t.Fatalf("got a < 1024 length string (%d)", len(n))
		}
	}

	for i := 0; i < 1000; i++ {
		n := randomString(1024, 2024)
		if len(n) > 2024 {
			t.Fatalf("got a > 2024 length string")
		}

		if len(n) < 1024 {
			t.Fatalf("got a < 1024 length string (%d)", len(n))
		}
	}
}

func TestRenderCluster(t *testing.T) {
	cluster := &api.ClusterInfo{
		Name:   "test",
		Leader: "S2",
		Replicas: []*api.PeerInfo{
			{Name: "S3", Current: false, Active: 30199700, Lag: 882130},
			{Name: "S1", Current: false, Active: 30202300, Lag: 882354},
		},
	}

	if result := renderCluster(cluster); result != "S1!, S2*, S3!" {
		t.Fatalf("invalid result: %s", result)
	}

	if result := renderCluster(&api.ClusterInfo{Name: "test"}); result != "" {
		t.Fatalf("invalid result: %q", result)
	}
}

func TestHostnameCompactor(t *testing.T) {
	names := []string{
		"broker-broker-2.broker-broker-ss.choria.svc.cluster.local",
		"broker-broker-0.broker-broker-ss.choria.svc.cluster.local",
		"broker-broker-1.broker-broker-ss.choria.svc.cluster.local",
	}

	result := compactStrings(names)
	if !cmp.Equal(result, []string{"broker-broker-2", "broker-broker-0", "broker-broker-1"}) {
		t.Fatalf("Recevied %#v", result)
	}

	names = []string{
		"broker-broker-2.broker-broker-ss.choria.svc.cluster.local1",
		"broker-broker-0.broker-broker-ss.choria.svc.cluster.local2",
		"broker-broker-1.broker-broker-ss.choria.svc.cluster.local3",
	}
	result = compactStrings(names)
	if !cmp.Equal(result, names) {
		t.Fatalf("Recevied %#v", result)
	}

	names = []string{
		"broker-broker-2.broker-broker-ss.choria.svc.cluster.local",
		"broker-broker-0.broker-broker-ss.choria.svc.cluster.local",
		"broker-broker-1.broker-broker-ss.other.svc.cluster.local",
	}
	result = compactStrings(names)
	if !cmp.Equal(result, []string{"broker-broker-2.broker-broker-ss.choria", "broker-broker-0.broker-broker-ss.choria", "broker-broker-1.broker-broker-ss.other"}) {
		t.Fatalf("Recevied %#v", result)
	}
}

func TestMultipleSort(t *testing.T) {
	if sortMultiSort(1, 1, "b", "a") {
		t.Fatalf("expected true")
	}

	if !sortMultiSort(1, 1, "a", "b") {
		t.Fatalf("expected false")
	}

	if sortMultiSort(1, 2, "a", "b") {
		t.Fatalf("expected false")
	}

	if !sortMultiSort(2, 1, "a", "b") {
		t.Fatalf("expected true")
	}
}
