// SPDX-License-Identifier: GPL-3.0-only

package tui

import (
	"fmt"
	"strings"
	"time"

	"ruddervirt-setup/internal/status"
)

// serviceStatusLine formats the home screen's Services row - one line, state
// conveyed by the bullet color (see StateBullet), or "" until
// fetchServiceStatusesCmd's first result lands (package main).
func serviceStatusLine(statuses []status.ServiceStatus) string {
	if len(statuses) == 0 {
		return ""
	}
	parts := make([]string, len(statuses))
	for i, st := range statuses {
		parts[i] = fmt.Sprintf("%s %s", StateBullet(st.State), st.Name)
	}
	return strings.Join(parts, "   ")
}

// RenderHomeStatus formats the home screen's combined "Status" block:
// Services (serviceStatusLine) and System (hostStatsLine) under one header
// instead of two, saving vertical space for the menu below. Renders nothing
// until at least one row has something to show. This is the presentation
// half of the former status.go/hoststats.go; the fetch half lives in
// internal/status, which this package imports (domain packages must never
// import internal/tui, so the dependency only runs tui -> status).
func RenderHomeStatus(statuses []status.ServiceStatus, hs status.HostStats, updatedAt time.Time, termWidth int) string {
	svcLine := serviceStatusLine(statuses)
	sysLine := hostStatsLine(hs)
	if svcLine == "" && sysLine == "" {
		return ""
	}
	header := "Status"
	if !updatedAt.IsZero() {
		header = fmt.Sprintf("Status (updated %s ago)", formatAge(time.Since(updatedAt)))
	}
	var b strings.Builder
	b.WriteString(HelpStyle.Render(header) + "\n")
	if svcLine != "" {
		// WrapIndented, not "  "+svcLine, so a narrow terminal wraps this
		// line cleanly instead of cutting it off.
		b.WriteString(WrapIndented(svcLine, 2, termWidth) + "\n")
	}
	if sysLine != "" {
		b.WriteString(WrapIndented(sysLine, 2, termWidth) + "\n")
	}
	b.WriteString("\n")
	return b.String()
}

// formatAge renders d for the home screen's "updated ... ago" hint, rounded
// to whole seconds/minutes.
func formatAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return d.Round(time.Second).String()
}

// formatGiB renders a GiB figure for the home screen's Mem row, switching to
// TiB above 1024 so a well-stocked server's total isn't an unwieldy 4-5
// digit number.
func formatGiB(v float64) string {
	if v >= 1024 {
		return fmt.Sprintf("%.1f TiB", v/1024)
	}
	return fmt.Sprintf("%.0f GiB", v)
}

// formatDiskUsage renders the home screen's Disk figure as
// "MGi used/NGi total - X% (YGi free)" (used%, colored via
// StyleUsagePercent). Leads with "used", like Mem, so the number matches the
// colored percent next to it rather than reading as its inverse.
func formatDiskUsage(freeGiB, totalGiB float64) string {
	usedGiB := totalGiB - freeGiB
	usedPercent := 0.0
	if totalGiB > 0 {
		usedPercent = usedGiB / totalGiB * 100
	}
	return fmt.Sprintf("%.0fGi used/%.0fGi total - %s (%.0fGi free)", usedGiB, totalGiB, StyleUsagePercent(usedPercent), freeGiB)
}

// hostStatsLine formats the home screen's System row - CPU/mem/disk/VM
// figures joined on one line, or "" until fetchHostStatsCmd's first result
// (package main) has something to show.
func hostStatsLine(hs status.HostStats) string {
	var parts []string
	if hs.CPUKnown {
		parts = append(parts, fmt.Sprintf("CPU %s", StyleUsagePercent(hs.CPUPercent)))
	}
	if hs.MemKnown {
		parts = append(parts, fmt.Sprintf("Mem %s (%s / %s)", StyleUsagePercent(hs.MemPercent), formatGiB(hs.MemUsedGiB), formatGiB(hs.MemTotalGiB)))
	}
	if hs.DiskKnown {
		parts = append(parts, fmt.Sprintf("Disk %s", formatDiskUsage(hs.DiskFreeGiB, hs.DiskTotalGiB)))
	}
	if hs.VMKnown {
		parts = append(parts, fmt.Sprintf("VMs %d running / %d total", hs.VMRunning, hs.VMTotal))
	}
	return strings.Join(parts, "   ")
}
