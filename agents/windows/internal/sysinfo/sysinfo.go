// Package sysinfo samples the cheap telemetry that rides on each heartbeat.
package sysinfo

import (
	"fmt"
	"os"
	"runtime"
	"time"

	"drs/agent/windows/internal/protocol"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
)

// CPUPercent samples total CPU utilisation. It blocks for the sample window, which is
// why the heartbeat interval is measured in seconds and not milliseconds.
func CPUPercent() int {
	pct, err := cpu.Percent(500*time.Millisecond, false)
	if err != nil || len(pct) == 0 {
		return 0
	}
	return int(pct[0] + 0.5)
}

// RAMPercent reports used memory as a percentage.
func RAMPercent() int {
	vm, err := mem.VirtualMemory()
	if err != nil {
		return 0
	}
	return int(vm.UsedPercent + 0.5)
}

// Info describes the machine. Sent on every heartbeat so a renamed or upgraded device
// corrects itself without re-enrolling (SRS FR-2.3).
func Info() protocol.SysInfo {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	return protocol.SysInfo{
		Hostname: hostname,
		OS:       osVersion(),
		Platform: runtime.GOOS,
	}
}

func osVersion() string {
	info, err := host.Info()
	if err != nil {
		return runtime.GOOS
	}
	if info.PlatformVersion != "" {
		return fmt.Sprintf("%s %s (%s)", info.Platform, info.PlatformFamily, info.PlatformVersion)
	}
	return info.Platform
}
