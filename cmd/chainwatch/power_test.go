package main

import (
	"math"
	"strings"
	"testing"
)

func TestSafeToStop(t *testing.T) {
	// The same cases are checked in scripts/ops/lib_test.sh. CometBFT
	// needs strictly more than 2/3 remaining, so exactly 1/3 is unsafe.
	cases := []struct {
		total, power int64
		want         bool
	}{
		{100, 25, true},
		{100, 33, true},  // remaining 67: 201 > 200
		{100, 34, false}, // remaining 66: 198 < 200
		{300, 100, false},
		{3, 1, false},
		{100, 0, true},
		{100, 100, false},
		{0, 0, false},
		{100, 101, false},
		{-1, 1, false},
		{100, 40, false},
		{100, 20, true},
		{math.MaxInt64, 1, true},
		{math.MaxInt64, math.MaxInt64 / 2, false},
	}
	for _, tc := range cases {
		if got := SafeToStop(tc.total, tc.power); got != tc.want {
			t.Errorf("SafeToStop(%d, %d) = %v, want %v", tc.total, tc.power, got, tc.want)
		}
	}
}

func TestHasQuorum(t *testing.T) {
	if !HasQuorum(67, 100) || HasQuorum(66, 100) || HasQuorum(2, 3) {
		t.Fatalf("quorum boundaries: 67/100 %v, 66/100 %v, 2/3 %v", HasQuorum(67, 100), HasQuorum(66, 100), HasQuorum(2, 3))
	}
}

func TestBuildReportFourEqualAndOneHeavy(t *testing.T) {
	equal := []valPower{
		{"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", 25},
		{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 25},
		{"cccccccccccccccccccccccccccccccccccccccc", 25},
		{"dddddddddddddddddddddddddddddddddddddddd", 25},
	}
	r, err := BuildReport(equal, 10, "aether-testnet-1", "seed")
	if err != nil {
		t.Fatal(err)
	}
	if !r.SafeToStopAnyOne || len(r.Validators) != 4 || r.Validators[0].Address != strings.Repeat("A", 40) {
		t.Fatalf("report = %+v", r)
	}
	text := FormatReport(r)
	if !strings.Contains(text, "any single validator can be stopped") {
		t.Fatalf("text = %s", text)
	}

	heavy := []valPower{
		{"B7F08625AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", 40},
		{"F94934BDAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", 20},
		{"CCA35D28AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", 20},
		{"BCEF78B5AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", 20},
	}
	r, err = BuildReport(heavy, 11, "", "sync3")
	if err != nil {
		t.Fatal(err)
	}
	if r.SafeToStopAnyOne {
		t.Fatal("40% validator reported safe for every stop")
	}
	c, err := r.Find("b7f08625")
	if err != nil {
		t.Fatal(err)
	}
	if c.SafeToStop || c.Share != "0.4000" || c.RemainingShare != "0.6000" {
		t.Fatalf("heavy choice = %+v", c)
	}
	light, err := r.Find("F94934BD")
	if err != nil {
		t.Fatal(err)
	}
	if !light.SafeToStop {
		t.Fatalf("20%% choice = %+v", light)
	}
	if _, err := r.Find("AAAAAAAA"); err == nil {
		t.Fatal("missing validator matched")
	}
}

func TestFindPrefixMustBeUnique(t *testing.T) {
	r, err := BuildReport([]valPower{
		{"ABCDEF0123456789AAAAAAAAAAAAAAAAAAAAAAAA", 50},
		{"ABCDEF9999999999AAAAAAAAAAAAAAAAAAAAAAAA", 50},
	}, 1, "", "n")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Find("ABCDEF"); err == nil {
		t.Fatal("ambiguous prefix accepted")
	}
	c, err := r.Find("ABCDEF0123456789")
	if err != nil {
		t.Fatal(err)
	}
	// Exactly half is unsafe: the other half is not more than 2/3.
	if c.SafeToStop {
		t.Fatal("half the power was marked safe")
	}
}
