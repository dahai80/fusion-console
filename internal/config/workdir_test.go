package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTildeWorkspaceCreation(t *testing.T) {
	for _, kind := range []string{"agent", "worker"} {
		t.Run(kind, func(t *testing.T) {
			t.Chdir(t.TempDir())
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			const workDir = "~/test/snake"
			cfg := Config{Agent: []AgentSettings{{Name: "test", WorkDir: workDir}}}
			var got string
			if kind == "agent" {
				got = cfg.ResolveWorkDir("test")
			} else {
				got = cfg.ResolveWorkerDir(WorkerConfig{Name: "test", WorkDir: workDir})
			}
			want := filepath.Join(home, "test", "snake")
			if got != want {
				t.Errorf("resolved workspace = %q, want %q", got, want)
			}
			if info, err := os.Stat(want); err != nil || !info.IsDir() {
				t.Errorf("expected a workspace directory at %q: %v", want, err)
			}
			if _, err := os.Lstat("~"); !os.IsNotExist(err) {
				t.Errorf("expected no literal ~ in cwd, got %v", err)
			}
		})
	}
}
