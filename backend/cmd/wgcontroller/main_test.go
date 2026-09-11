package main

import "testing"

func TestValidClientIPIsRoomScoped(t *testing.T) {
	tests := []struct {
		room int64
		ip   string
		want bool
	}{
		{7, "10.222.7.10", true},
		{7, "10.222.8.10", false},
		{8, "10.222.8.109", true},
		{8, "10.222.7.10", false},
		{7, "10.222.7.9", false},
		{7, "10.222.7.110", false},
		{6, "10.222.7.10", false},
		{7, "2001:db8::7", false},
	}
	for _, test := range tests {
		if got := validClientIP(test.room, test.ip); got != test.want {
			t.Fatalf("validClientIP(%d, %q) = %v, want %v", test.room, test.ip, got, test.want)
		}
	}
}
