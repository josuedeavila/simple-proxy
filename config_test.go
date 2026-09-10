package proxy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadConfigFromBytes tests loading configuration from a byte slice
func TestLoadConfigFromBytes(t *testing.T) {
	tests := []struct {
		name        string
		yaml        string
		wantErr     bool
		wantPort    int
		wantTimeout int
		wantRoutes  int
	}{
		{
			name: "explicit server settings",
			yaml: `
server:
  port: 9090
  timeout: 60
routes:
  - path: /test
    target: http://example.com
    methods: [GET]
`,
			wantPort:    9090,
			wantTimeout: 60,
			wantRoutes:  1,
		},
		{
			name: "defaults applied when server block is absent",
			yaml: `
routes:
  - path: /test
    target: http://example.com
`,
			wantPort:    8000,
			wantTimeout: 30,
			wantRoutes:  1,
		},
		{
			name:        "empty document",
			yaml:        "",
			wantPort:    8000,
			wantTimeout: 30,
		},
		{
			name:    "malformed yaml",
			yaml:    "server: {port: [1,",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config, err := LoadConfigFromBytes([]byte(tt.yaml))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadConfigFromBytes() error = %v", err)
			}
			if config.Server.Port != tt.wantPort {
				t.Errorf("port = %d, want %d", config.Server.Port, tt.wantPort)
			}
			if config.Server.Timeout != tt.wantTimeout {
				t.Errorf("timeout = %d, want %d", config.Server.Timeout, tt.wantTimeout)
			}
			if len(config.Routes) != tt.wantRoutes {
				t.Errorf("routes = %d, want %d", len(config.Routes), tt.wantRoutes)
			}
		})
	}
}

// TestLoadConfigFromFile tests loading a single configuration file
func TestLoadConfigFromFile(t *testing.T) {
	dir := t.TempDir()

	valid := filepath.Join(dir, "valid.yaml")
	writeFile(t, valid, `
server:
  port: 7070
routes:
  - path: /one
    target: http://one.example.com
`)

	invalid := filepath.Join(dir, "invalid.yaml")
	writeFile(t, invalid, "routes: [{path: /one")

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{name: "valid file", path: valid},
		{name: "missing file", path: filepath.Join(dir, "nope.yaml"), wantErr: true},
		{name: "malformed file", path: invalid, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config, err := LoadConfigFromFile(tt.path)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadConfigFromFile() error = %v", err)
			}
			if config.Server.Port != 7070 {
				t.Errorf("port = %d, want 7070", config.Server.Port)
			}
			if config.Server.Timeout != 30 {
				t.Errorf("timeout = %d, want the default 30", config.Server.Timeout)
			}
		})
	}
}

// TestLoadConfigFromDir tests loading and merging every file in a directory
func TestLoadConfigFromDir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "config1.yaml"), `
server:
  port: 9091
routes:
  - path: /route1
    target: http://target1.com
`)
	writeFile(t, filepath.Join(dir, "config2.yml"), `
routes:
  - path: /route2
    target: http://target2.com
`)

	config, err := LoadConfigFromDir(dir)
	if err != nil {
		t.Fatalf("LoadConfigFromDir() error = %v", err)
	}
	if config.Server.Port != 9091 {
		t.Errorf("port = %d, want 9091", config.Server.Port)
	}
	if len(config.Routes) != 2 {
		t.Fatalf("routes = %d, want 2", len(config.Routes))
	}
	// Files are merged in lexical order, so route1 comes first.
	if config.Routes[0].Path != "/route1" || config.Routes[1].Path != "/route2" {
		t.Errorf("routes = %v, want /route1 then /route2", config.Routes)
	}
}

// TestLoadConfigFromDir_Errors verifies that a bad directory is reported
// instead of being silently skipped.
func TestLoadConfigFromDir_Errors(t *testing.T) {
	empty := t.TempDir()

	broken := t.TempDir()
	writeFile(t, filepath.Join(broken, "ok.yaml"), "routes: []")
	writeFile(t, filepath.Join(broken, "zbad.yaml"), "routes: [{path: /a")

	tests := []struct {
		name    string
		dir     string
		wantMsg string
	}{
		{name: "no yaml files", dir: empty, wantMsg: "no YAML files found"},
		{name: "one malformed file", dir: broken, wantMsg: "zbad.yaml"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadConfigFromDir(tt.dir)
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("error = %q, want it to mention %q", err, tt.wantMsg)
			}
		})
	}
}

// TestMergeConfigs verifies route concatenation and server-setting precedence
func TestMergeConfigs(t *testing.T) {
	tests := []struct {
		name        string
		configs     []*Config
		wantPort    int
		wantTimeout int
		wantPaths   []string
	}{
		{
			name:        "no configs falls back to defaults",
			configs:     nil,
			wantPort:    8000,
			wantTimeout: 30,
		},
		{
			name: "first config that sets a field wins",
			configs: []*Config{
				{Server: ServerConfig{Port: 0, Timeout: 15}, Routes: []Route{{Path: "/a"}}},
				{Server: ServerConfig{Port: 9000, Timeout: 99}, Routes: []Route{{Path: "/b"}}},
			},
			wantPort:    9000,
			wantTimeout: 15,
			wantPaths:   []string{"/a", "/b"},
		},
		{
			name: "routes keep their input order",
			configs: []*Config{
				{Routes: []Route{{Path: "/a"}, {Path: "/b"}}},
				{Routes: []Route{{Path: "/c"}}},
			},
			wantPort:    8000,
			wantTimeout: 30,
			wantPaths:   []string{"/a", "/b", "/c"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			merged := mergeConfigs(tt.configs)

			if merged.Server.Port != tt.wantPort {
				t.Errorf("port = %d, want %d", merged.Server.Port, tt.wantPort)
			}
			if merged.Server.Timeout != tt.wantTimeout {
				t.Errorf("timeout = %d, want %d", merged.Server.Timeout, tt.wantTimeout)
			}
			if got := paths(merged.Routes); !equal(got, tt.wantPaths) {
				t.Errorf("routes = %v, want %v", got, tt.wantPaths)
			}
		})
	}
}

// TestMergeConfigs_DoesNotMutateInput guards against merging leaking defaults
// or routes back into the configs it was given.
func TestMergeConfigs_DoesNotMutateInput(t *testing.T) {
	first := &Config{Routes: []Route{{Path: "/a"}}}
	second := &Config{Server: ServerConfig{Port: 9000}, Routes: []Route{{Path: "/b"}}}

	mergeConfigs([]*Config{first, second})

	if first.Server.Port != 0 {
		t.Errorf("first config port = %d, want it left at 0", first.Server.Port)
	}
	if len(first.Routes) != 1 {
		t.Errorf("first config routes = %d, want it left at 1", len(first.Routes))
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func paths(routes []Route) []string {
	var out []string
	for _, route := range routes {
		out = append(out, route.Path)
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
