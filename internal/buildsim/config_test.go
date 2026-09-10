package buildsim

import (
	"testing"
	"time"
)

func TestConfigFromEnv(t *testing.T) {
	tests := []struct {
		name        string
		url         string
		timeout     string
		wantErr     bool
		wantBase    string
		wantTimeout time.Duration
	}{
		{
			name:        "defaults",
			url:         "http://127.0.0.1:9090",
			wantBase:    "http://127.0.0.1:9090",
			wantTimeout: DefaultTimeout,
		},
		{
			name:        "explicit timeout",
			url:         "http://buildsim:9090",
			timeout:     "2s",
			wantBase:    "http://buildsim:9090",
			wantTimeout: 2 * time.Second,
		},
		{
			name:     "trailing slash trimmed",
			url:      "http://127.0.0.1:9090/",
			wantBase: "http://127.0.0.1:9090",
		},
		{name: "missing url", url: "", wantErr: true},
		{name: "bad scheme", url: "ftp://127.0.0.1:9090", wantErr: true},
		{name: "no host", url: "http://", wantErr: true},
		{name: "bad timeout", url: "http://x:9090", timeout: "soon", wantErr: true},
		{name: "zero timeout", url: "http://x:9090", timeout: "0s", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("BUILDSIM_URL", tt.url)
			t.Setenv("BUILDSIM_TIMEOUT", tt.timeout)

			cfg, err := ConfigFromEnv()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got cfg %+v", cfg)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantBase != "" && cfg.BaseURL != tt.wantBase {
				t.Errorf("BaseURL = %q, want %q", cfg.BaseURL, tt.wantBase)
			}
			if tt.wantTimeout != 0 && cfg.Timeout != tt.wantTimeout {
				t.Errorf("Timeout = %s, want %s", cfg.Timeout, tt.wantTimeout)
			}
		})
	}
}
