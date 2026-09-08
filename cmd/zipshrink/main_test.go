package main

import (
	"testing"
)

func TestParseBytes(t *testing.T) {
	tests := []struct {
		input    string
		expected int64
		wantErr  bool
	}{
		{"100MB", 100 * 1024 * 1024, false},
		{"512mb", 512 * 1024 * 1024, false},
		{"1GB", 1024 * 1024 * 1024, false},
		{"1024", 1024, false},
		{"500 KB", 500 * 1024, false},
		{"1.5GB", int64(1.5 * 1024 * 1024 * 1024), false},
		{"invalid", 0, true},
		{"", 0, true},
	}

	for _, tt := range tests {
		got, err := parseBytes(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseBytes(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			continue
		}
		if !tt.wantErr && got != tt.expected {
			t.Errorf("parseBytes(%q) = %d, want %d", tt.input, got, tt.expected)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		input    int64
		expected string
	}{
		{500, "500 B"},
		{1024, "1.00 KB"},
		{10 * 1024 * 1024, "10.00 MB"},
		{5 * 1024 * 1024 * 1024, "5.00 GB"},
	}

	for _, tt := range tests {
		got := formatBytes(tt.input)
		if got != tt.expected {
			t.Errorf("formatBytes(%d) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
