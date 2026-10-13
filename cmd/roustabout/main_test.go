package main

import "testing"

func TestBuildAppRegistersVersion(t *testing.T) {
	app := buildApp()
	if _, ok := app.GetCommand("version"); !ok {
		t.Fatal("version command not registered")
	}
}
