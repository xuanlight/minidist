package main

import "testing"

func TestGreet(t *testing.T) {
	got, err := greet("alice")
	if err != nil || got != "hello, alice" {
		t.Fatalf("greet(alice) = %q, %v", got, err)
	}
	if _, err := greet(""); err == nil {
		t.Fatal(`greet("") should return an error`)
	}
}

func TestRun(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{name: "no flags", args: nil},
		{name: "space form", args: []string{"-name", "alice"}},
		{name: "equals form", args: []string{"-name=bob"}},
		{name: "empty name", args: []string{"-name", ""}, wantErr: true},
		{name: "missing value", args: []string{"-name"}, wantErr: true},
		{name: "help", args: []string{"-h"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := run(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("run(%q) error = %v, wantErr = %v", tt.args, err, tt.wantErr)
			}
		})
	}
}
