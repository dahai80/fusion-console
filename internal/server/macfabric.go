// macfabric lifecycle (design v2 §6): macOS Apple Silicon only. The console
// detects (port + launchd label), starts/stops via launchctl, and for fresh
// installs runs the local release-install.sh when present (stage 1 of the
// two-stage strategy) or returns guidance.
package server

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"time"
)

const (
	macfabricPort      = 8999
	macfabricLaunchdID = "com.macfabric.gateway"
	macfabricRepoHint  = "https://github.com/vincentlau2046/MacFabric"
)

// macfabricSourceRepo returns the local MacFabric source checkout when
// present (dev-machine scenario for the stage-1 installer).
func macfabricSourceRepo() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Documents", "MacFabric")
}

func macfabricInstallScript() string {
	return filepath.Join(macfabricSourceRepo(), "scripts", "release-install.sh")
}

// macfabricRunning probes the gateway port.
func macfabricRunning() bool {
	return probeTCPEndpoint(fmt.Sprintf("http://127.0.0.1:%d", macfabricPort), 500*time.Millisecond)
}

// macfabricServiceRegistered checks the launchd label exists.
func macfabricServiceRegistered() bool {
	out, err := exec.Command("launchctl", "print", "gui/"+strconv.Itoa(os.Getuid())+"/"+macfabricLaunchdID).CombinedOutput()
	return err == nil && len(out) > 0
}

// macfabricStart kicks the launchd service and polls the port.
func macfabricStart(ctx context.Context) (string, error) {
	if macfabricRunning() {
		return "already running on :" + strconv.Itoa(macfabricPort), nil
	}
	if !macfabricServiceRegistered() {
		return "", fmt.Errorf("macfabric service %s is not registered — install MacFabric first", macfabricLaunchdID)
	}
	if err := exec.CommandContext(ctx, "launchctl", "kickstart", "-k",
		"gui/"+strconv.Itoa(os.Getuid())+"/"+macfabricLaunchdID).Run(); err != nil {
		return "", fmt.Errorf("launchctl kickstart: %w", err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("macfabric start cancelled")
		default:
		}
		if macfabricRunning() {
			return "started (" + macfabricLaunchdID + ")", nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return "", fmt.Errorf("macfabric did not answer on :%d within 20s", macfabricPort)
}

// macfabricStop boots the service out (launchctl bootout of the gui domain).
func macfabricStop() (string, error) {
	if !macfabricServiceRegistered() {
		if macfabricRunning() {
			return "", fmt.Errorf("macfabric is answering on :%d but the launchd label is missing — stop it from the MacFabric app", macfabricPort)
		}
		return "not running", nil
	}
	if err := exec.Command("launchctl", "bootout",
		"gui/"+strconv.Itoa(os.Getuid())+"/"+macfabricLaunchdID).Run(); err != nil {
		return "", fmt.Errorf("launchctl bootout: %w", err)
	}
	return "stopped (" + macfabricLaunchdID + ")", nil
}

// macfabricInstall runs the local release-install.sh (stage 1, design §6.1)
// streaming through the ragInstallState log; on a fresh Mac without the
// repo it returns guidance instead of pretending.
func (s *Server) macfabricInstall(ctx context.Context, st *ragInstallState) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("macfabric is macOS (Apple Silicon) only")
	}
	script := macfabricInstallScript()
	if _, err := os.Stat(script); err != nil {
		return fmt.Errorf("no local MacFabric source repo found — install MacFabric from %s (dmg), then retry", macfabricRepoHint)
	}
	st.setPhase("downloading")
	st.logf("running %s", script)
	st.setPhase("installing")
	if err := runStream(ctx, st, macfabricSourceRepo(), "bash", script); err != nil {
		return fmt.Errorf("macfabric release-install failed: %v", err)
	}
	if !macfabricRunning() {
		// The installer registers the launchd service; give it a kick.
		if _, err := macfabricStart(ctx); err != nil {
			st.logf("post-install start: %v", err)
		}
	}
	st.logf("macfabric installed; gateway on :%d", macfabricPort)
	st.setPhase("done")
	return nil
}
