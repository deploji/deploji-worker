package utils

import (
	"testing"
)

func TestAnsiColor(t *testing.T) {
	var fibTests = []struct {
		input    string
		expected string
	}{
		{"[1;30mFAILED - RETRYING: command (49 retries left).[0m", "<span class=\"ansi-bright-black\">FAILED - RETRYING: command (49 retries left).</span>"},
		{"RETRYING:\tcommand (49 retries left)", "RETRYING:&nbsp&nbsp&nbsp&nbspcommand (49 retries left)"},
	}
	for _, tt := range fibTests {
		actual := AnsiColor(tt.input)
		if actual != tt.expected {
			t.Errorf("AnsiColor(%s): expected %s, actual %s", tt.input, tt.expected, actual)
		}
	}
}
