package cli

import "testing"

func TestEnvNumber(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    int64
		wantErr bool
	}{
		{"unset", "", 7, false},
		{"number", "16777216", 16777216, false},
		{"spaces", " 42 ", 42, false},
		{"units are not accepted", "32MB", 7, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("LANESH_TEST_NUMBER", tt.value)
			errs := map[string]error{}
			if got := envNumber("LANESH_TEST_NUMBER", "test-number", 7, errs); got != tt.want {
				t.Fatalf("got %d, want %d", got, tt.want)
			}
			if (len(errs) > 0) != tt.wantErr {
				t.Fatalf("errors = %v, want error %v", errs, tt.wantErr)
			}
		})
	}
}

func TestEnvBool(t *testing.T) {
	tests := []struct {
		value   string
		want    bool
		wantErr bool
	}{
		{"", true, false},
		{"false", false, false},
		{"0", false, false},
		{" true ", true, false},
		{"off", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			t.Setenv("LANESH_TEST_BOOL", tt.value)
			errs := map[string]error{}
			if got := envBool("LANESH_TEST_BOOL", "test-bool", true, errs); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			if (len(errs) > 0) != tt.wantErr {
				t.Fatalf("errors = %v, want error %v", errs, tt.wantErr)
			}
		})
	}
}
