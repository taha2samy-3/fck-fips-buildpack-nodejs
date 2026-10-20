package env

import "testing"

func TestBool(t *testing.T) {
	tests := []struct {
		value   string
		want    bool
		wantErr bool
	}{
		{"", false, false},
		{"true", true, false},
		{" TRUE ", true, false},
		{"1", true, false},
		{"false", false, false},
		{"yes", false, true}, // a typo must not silently mean false
	}
	for _, tt := range tests {
		t.Setenv("X_BOOL", tt.value)
		got, err := Bool("X_BOOL")
		if got != tt.want || (err != nil) != tt.wantErr {
			t.Errorf("Bool(%q) = %t, %v", tt.value, got, err)
		}
	}

	t.Setenv("X_BOOL", "")
	if v, _ := BoolDefault("X_BOOL", true); !v {
		t.Error("BoolDefault should use the default when unset")
	}
	if String("X_BOOL", "def") != "def" {
		t.Error("String should use the default when empty")
	}
}
