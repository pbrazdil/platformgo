package main

import (
	"reflect"
	"testing"
)

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/app/tests/it/identity/e2e_admin_login.rs:70
// test: cli_dispatches_through_the_command_layer
func TestLifecycleCLICompatibility(t *testing.T) {
	tests := []struct {
		arguments []string
		want      command
	}{
		{arguments: []string{"serve"}, want: command{name: "serve"}},
		{arguments: []string{"migrate"}, want: command{name: "migrate"}},
		{arguments: []string{"doctor"}, want: command{name: "doctor"}},
		{
			arguments: []string{
				"worker", "--handlers=outbox-publisher,event-consumer",
				"--handlers", "jobs",
			},
			want: command{
				name: "worker",
				handlers: []string{
					"outbox-publisher,event-consumer",
					"jobs",
				},
			},
		},
	}
	for _, test := range tests {
		got, err := parseCLI(test.arguments)
		if err != nil {
			t.Fatalf("%v: %v", test.arguments, err)
		}
		if !reflect.DeepEqual(got, test.want) {
			t.Fatalf("%v: got %#v, want %#v", test.arguments, got, test.want)
		}
	}
}

func TestLifecycleCLIRejectsUnknownOrMissingRoles(t *testing.T) {
	for _, arguments := range [][]string{
		{"unknown"},
		{"worker"},
		{"worker", "--handlers"},
		{"serve", "extra"},
	} {
		if _, err := parseCLI(arguments); err == nil {
			t.Fatalf("%v accepted", arguments)
		}
	}
}
