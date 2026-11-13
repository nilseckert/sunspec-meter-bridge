package main

import "testing"

func TestExampleConfig(t *testing.T) {
	if _, err := LoadConfig("config.example.yaml"); err != nil {
		t.Fatal(err)
	}
}
