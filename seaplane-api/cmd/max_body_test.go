package main

import "testing"

func TestParseMaxRequestBodyBytes(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		wantN  int64
		wantOK bool
	}{
		{"empty string", "", 0, false},
		{"valid positive", "1048576", 1048576, true},
		{"valid small", "64", 64, true},
		{"zero rejected", "0", 0, false},
		{"negative rejected", "-1", 0, false},
		{"non-numeric", "abc", 0, false},
		{"float rejected", "1.5", 0, false},
		{"whitespace", " ", 0, false},
		{"very large", "10737418240", 10737418240, true}, // 10 GiB — valid even if impractical
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotN, gotOK := parseMaxRequestBodyBytes(tt.input)
			if gotOK != tt.wantOK {
				t.Errorf("ok: got %v, want %v", gotOK, tt.wantOK)
			}
			if gotN != tt.wantN {
				t.Errorf("n: got %d, want %d", gotN, tt.wantN)
			}
		})
	}
}
