package launch

import (
	"reflect"
	"testing"
)

func TestCompute(t *testing.T) {
	const mb = 1024 * 1024
	tests := []struct {
		name  string
		env   map[string]string
		limit int64
		want  map[string]string
	}{
		{
			name: "defaults",
			want: map[string]string{"NODE_OPTIONS": "--use-openssl-ca"},
		},
		{
			name: "existing options are kept and CA choice respected",
			env:  map[string]string{"NODE_OPTIONS": "--use-bundled-ca --enable-source-maps"},
			want: map[string]string{},
		},
		{
			name: "fips",
			env:  map[string]string{"BPI_NODE_FORCE_FIPS": "true"},
			want: map[string]string{"NODE_OPTIONS": "--use-openssl-ca --force-fips"},
		},
		{
			name: "fips already present",
			env:  map[string]string{"BPI_NODE_FORCE_FIPS": "true", "NODE_OPTIONS": "--force-fips --use-openssl-ca"},
			want: map[string]string{},
		},
		{
			name:  "memory limit without optimisation only reports it",
			limit: 512 * mb,
			want:  map[string]string{"MEMORY_AVAILABLE": "512", "NODE_OPTIONS": "--use-openssl-ca"},
		},
		{
			name:  "optimised heap is 75% of the limit",
			env:   map[string]string{"OPTIMIZE_MEMORY": "true"},
			limit: 1024 * mb,
			want:  map[string]string{"MEMORY_AVAILABLE": "1024", "NODE_OPTIONS": "--use-openssl-ca --max-old-space-size=768"},
		},
		{
			name:  "MEMORY_AVAILABLE overrides the cgroup",
			env:   map[string]string{"OPTIMIZE_MEMORY": "true", "MEMORY_AVAILABLE": "400"},
			limit: 1024 * mb,
			want:  map[string]string{"MEMORY_AVAILABLE": "400", "NODE_OPTIONS": "--use-openssl-ca --max-old-space-size=300"},
		},
		{
			name:  "user heap size wins",
			env:   map[string]string{"OPTIMIZE_MEMORY": "true", "NODE_OPTIONS": "--max_old_space_size=100"},
			limit: 1024 * mb,
			want:  map[string]string{"MEMORY_AVAILABLE": "1024", "NODE_OPTIONS": "--max_old_space_size=100 --use-openssl-ca"},
		},
		{
			name: "debugger",
			env:  map[string]string{"BPL_DEBUG_ENABLED": "true"},
			want: map[string]string{"NODE_OPTIONS": "--use-openssl-ca --inspect=127.0.0.1:9229"},
		},
		{
			name: "debugger on a custom port",
			env:  map[string]string{"BPL_DEBUG_ENABLED": "true", "BPL_DEBUG_PORT": "9000"},
			want: map[string]string{"NODE_OPTIONS": "--use-openssl-ca --inspect=127.0.0.1:9000"},
		},
		{
			name: "user inspector wins",
			env:  map[string]string{"BPL_DEBUG_ENABLED": "true", "NODE_OPTIONS": "--inspect=0.0.0.0:9229 --use-openssl-ca"},
			want: map[string]string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Compute(func(k string) string { return tt.env[k] }, func() int64 { return tt.limit })
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Compute = %v, want %v", got, tt.want)
			}
		})
	}
}
