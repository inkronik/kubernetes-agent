package main

import "testing"

func TestShouldPrintVersion(t *testing.T) {
	testCases := []struct {
		name     string
		args     []string
		expected bool
	}{
		{name: "long flag", args: []string{"inkronik-k8s-agent", "--version"}, expected: true},
		{name: "command", args: []string{"inkronik-k8s-agent", "version"}, expected: true},
		{name: "no argument", args: []string{"inkronik-k8s-agent"}, expected: false},
		{name: "unknown argument", args: []string{"inkronik-k8s-agent", "--help"}, expected: false},
		{name: "extra argument", args: []string{"inkronik-k8s-agent", "--version", "extra"}, expected: false},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			actual := shouldPrintVersion(testCase.args)
			if actual != testCase.expected {
				t.Fatalf("expected %t, got %t", testCase.expected, actual)
			}
		})
	}
}
