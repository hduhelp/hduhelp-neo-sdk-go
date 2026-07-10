package main

import "testing"

func TestParameterSetterForHeaders(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{name: "X-Staff-Id", want: "StaffID"},
		{name: "User-Agent", want: "UserAgent"},
		{name: "x-device-id", want: "DeviceID"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parameterSetter(param{Name: tt.name, In: "header"})
			if got != tt.want {
				t.Fatalf("parameterSetter() = %q, want %q", got, tt.want)
			}
		})
	}
}
