// SPDX-License-Identifier: GPL-3.0-only

// Package status holds the pure data-fetch half of the former status.go/
// hoststats.go - probing systemd/kubectl for the home screen's "Services"
// and "System" summaries, with zero rendering/model coupling. Presentation
// lives in internal/tui/render.go instead, since domain packages must never
// import internal/tui - see that file's doc comment.
package status

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"ruddervirt-setup/internal/exec"
	"ruddervirt-setup/internal/k3s"
	"ruddervirt-setup/internal/storage"
)

// StatusCheckTimeout bounds every check below - short enough that a
// slow/unreachable API can't make the home screen feel stuck (these run as
// a background tea.Cmd). Padded past each kubectl call's own --timeout=1s
// so process-startup/TLS overhead alone can't read as a cancelled failure.
// Exported: package main's aileron_bridge.go (stabilizerChartPresent) needs
// this too.
const StatusCheckTimeout = 5 * time.Second

// ServiceStatus is one row of the home screen's "Services" summary.
type ServiceStatus struct {
	Name  string
	State string
}

// nonInteractiveSucceeds runs a bounded, non-interactive command and
// reports only whether it exited clean - the plain existence/readiness
// probe every check below needs.
func nonInteractiveSucceeds(name string, args ...string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), StatusCheckTimeout)
	defer cancel()
	return exec.RunNonInteractive(ctx, name, args...).Run() == nil
}

// K3sServiceActive reports whether the k3s.service systemd unit is active.
// This is a mandatory gate before trusting ANY kubectl call, not just an
// optimization: before k3s is installed, /usr/local/bin/k3s is a
// placeholder text file with no `#!` shebang (see server.bu) - POSIX
// shell's ENOEXEC fallback silently treats it as a no-op script, so
// `kubectl` (which execs through that wrapper) reports a false SUCCESS
// instead of "k3s not found". Checking k3s.service first prevents that
// placeholder from ever being mistaken for a present/ready resource.
// Exported: internal/stabilizer's adopt.go and package main's
// stabilizer_bridge.go need this too.
func K3sServiceActive() bool {
	return nonInteractiveSucceeds("/usr/bin/systemctl", "is-active", "--quiet", "k3s.service")
}

// HaveNonInteractiveSudo reports whether a cached sudo ticket lets
// nonInteractiveSucceeds's checks reach kubectl/systemctl - if not (fresh
// boot, expired ticket), every check below would fail identically, so
// callers short-circuit to "unknown" instead of waiting out each timeout.
// Exported: package main's aileron_bridge.go (stabilizerChartPresent) needs
// this too.
func HaveNonInteractiveSudo() bool {
	if os.Getuid() == 0 {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), StatusCheckTimeout)
	defer cancel()
	return exec.DefaultRunner.Run(ctx, "sudo", "-n", "true") == nil
}

// FetchServiceStatuses is the synchronous body of package main's
// fetchServiceStatusesCmd, split out so it's callable without a running
// tea.Program (e.g. tests). Returns nil rather than probing
// systemctl/kubectl if the operator has never run "configure" - nothing
// meaningful to report on a fresh system.
//
// engine is cfg.Storage.Engine; configSaved/stabilizerPresent are injected
// (config.ConfigSaved, and stabilizerChartPresent, aileron_bridge.go) since
// this package can't import package main - see its fetchServiceStatuses
// adapter.
func FetchServiceStatuses(engine string, configSaved func() bool, stabilizerPresent func() bool) []ServiceStatus {
	if !configSaved() {
		return nil
	}

	names := []string{"k3s", "kube-ovn", fmt.Sprintf("storage (%s)", engine), "kubevirt", "aileron"}

	unknown := func() []ServiceStatus {
		out := make([]ServiceStatus, len(names))
		for i, n := range names {
			out[i] = ServiceStatus{Name: n, State: "unknown"}
		}
		return out
	}

	// No cached sudo ticket - every check would fail identically (each
	// burning StatusCheckTimeout), so report unknown up front.
	if !HaveNonInteractiveSudo() {
		return unknown()
	}

	const kubectlBin = "/usr/local/bin/kubectl"

	if !K3sServiceActive() {
		out := unknown()
		out[0].State = "not running"
		for i := 1; i < len(out); i++ {
			out[i].State = "not running"
		}
		return out
	}

	statuses := []ServiceStatus{{Name: "k3s", State: "running"}}

	kubeOvnReady := true
	for _, w := range k3s.KubeOvnCoreWorkloads {
		if !nonInteractiveSucceeds(kubectlBin, "-n", "kube-system", "rollout", "status", w.Kind+"/"+w.Name, "--timeout=1s") {
			kubeOvnReady = false
			break
		}
	}
	statuses = append(statuses, ServiceStatus{Name: "kube-ovn", State: readyState(kubeOvnReady)})

	statuses = append(statuses, ServiceStatus{
		Name:  fmt.Sprintf("storage (%s)", engine),
		State: readyState(storageEngineReady(kubectlBin, engine)),
	})

	// KubeVirt's and CDI's own operators set condition=Available once fully
	// rolled out - both must be true for the combined "kubevirt" row here.
	kubevirtReady := nonInteractiveSucceeds(kubectlBin, "-n", "kubevirt", "wait", "--for=condition=Available", "kubevirt/kubevirt", "--timeout=1s") &&
		nonInteractiveSucceeds(kubectlBin, "wait", "--for=condition=Available", "cdi/cdi", "--timeout=1s")
	statuses = append(statuses, ServiceStatus{Name: "kubevirt", State: readyState(kubevirtReady)})

	statuses = append(statuses, ServiceStatus{Name: "aileron", State: readyState(AileronReady(kubectlBin))})

	// Only shown for a stabilizer-managed install (see stabilizerPresent,
	// aileron_bridge.go's stabilizerChartPresent) - a plain self-hosted node
	// has no "stabilizer" Deployment, so unconditionally waiting on it would
	// read as permanently "not ready" for everyone else.
	if stabilizerPresent() {
		statuses = append(statuses, ServiceStatus{Name: "stabilizer", State: readyState(stabilizerReady(kubectlBin))})
	}

	return statuses
}

// stabilizerReady reports whether the stabilizer Deployment - alongside
// Aileron's own, in the same ruddervirt-system namespace - has become
// Available.
func stabilizerReady(kubectlBin string) bool {
	return nonInteractiveSucceeds(kubectlBin, "-n", "ruddervirt-system", "wait", "--for=condition=Available", "deployment.apps/stabilizer", "--timeout=1s")
}

// AileronReady reports whether Aileron itself is up, not just whether
// ruddervirt-setup's own install succeeded. Waiting on the
// "helm-install-aileron" Job (applyAileron, aileron_bridge.go) only reflects
// that install; once a "stabilizer" HelmChart takes over Aileron's
// management, that Job may no longer exist, which would wrongly read as
// permanently "not ready". Waiting on the Deployment directly (in
// ruddervirt-system, aileron-helmchart.yaml's targetNamespace) works either
// way, regardless of which chart put it there. Exported:
// internal/stabilizer's adopt.go and package main's stabilizer_bridge.go
// need this too.
func AileronReady(kubectlBin string) bool {
	return nonInteractiveSucceeds(kubectlBin, "-n", "ruddervirt-system", "wait", "--for=condition=Available", "deployment.apps/aileron", "--timeout=1s")
}

// storageEngineReady mirrors internal/k3s's applyStorageEngine per-engine
// readiness signal, just probed instantly instead of blocked on.
func storageEngineReady(kubectlBin, engine string) bool {
	switch engine {
	case "rook-ceph":
		return nonInteractiveSucceeds(kubectlBin, "-n", "rook-ceph", "wait", "--for=condition=Ready", "cephcluster/rook-ceph", "--timeout=1s")
	case "longhorn":
		return nonInteractiveSucceeds(kubectlBin, "-n", "longhorn-system", "wait", "--for=condition=Ready", "pods", "--all", "--timeout=1s")
	case "openebs":
		return nonInteractiveSucceeds(kubectlBin, "-n", "openebs", "wait", "--for=condition=Ready", "pods", "--all", "--timeout=1s")
	default:
		return false
	}
}

// storageEngineCapacity asks the storage engine itself how much space is
// left for VM data, unlike statfs("/") (this home screen's old approach):
// for rook-ceph there is no root-fs equivalent at all, since bluestore OSDs
// consume the raw partition directly, never mounted.
func storageEngineCapacity(kubectlBin, engine string) (freeGiB, totalGiB float64, ok bool) {
	switch engine {
	case "rook-ceph":
		return cephClusterCapacity(kubectlBin)
	case "longhorn":
		return longhornCapacity(kubectlBin)
	case "openebs":
		return openebsVGCapacity()
	default:
		return 0, 0, false
	}
}

// cephClusterCapacity reads the CephCluster CR's capacity summary - the
// rook operator periodically populates status.ceph.capacity from `ceph df`,
// so this needs nothing beyond a single kubectl get.
func cephClusterCapacity(kubectlBin string) (freeGiB, totalGiB float64, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), StatusCheckTimeout)
	defer cancel()
	out, err := exec.RunNonInteractive(ctx, kubectlBin, "get", "cephcluster", "-n", "rook-ceph", "rook-ceph",
		"-o", "jsonpath={.status.ceph.capacity.bytesAvailable} {.status.ceph.capacity.bytesTotal}").Output()
	if err != nil {
		return 0, 0, false
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		return 0, 0, false
	}
	avail, err1 := strconv.ParseFloat(fields[0], 64)
	total, err2 := strconv.ParseFloat(fields[1], 64)
	if err1 != nil || err2 != nil || total == 0 {
		return 0, 0, false
	}
	return avail / bytesPerGiB, total / bytesPerGiB, true
}

// longhornNodeList is the handful of nodes.longhorn.io CR fields
// longhornCapacity needs - each node's disks report capacity under
// status.diskStatus, summed across nodes/disks for a cluster-wide total.
type longhornNodeList struct {
	Items []struct {
		Status struct {
			DiskStatus map[string]struct {
				StorageAvailable int64 `json:"storageAvailable"`
				StorageMaximum   int64 `json:"storageMaximum"`
			} `json:"diskStatus"`
		} `json:"status"`
	} `json:"items"`
}

func longhornCapacity(kubectlBin string) (freeGiB, totalGiB float64, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), StatusCheckTimeout)
	defer cancel()
	out, err := exec.RunNonInteractive(ctx, kubectlBin, "get", "nodes.longhorn.io", "-n", "longhorn-system", "-o", "json").Output()
	if err != nil {
		return 0, 0, false
	}
	var list longhornNodeList
	if err := json.Unmarshal(out, &list); err != nil {
		return 0, 0, false
	}
	var freeBytes, totalBytes int64
	for _, item := range list.Items {
		for _, disk := range item.Status.DiskStatus {
			freeBytes += disk.StorageAvailable
			totalBytes += disk.StorageMaximum
		}
	}
	if totalBytes == 0 {
		return 0, 0, false
	}
	return float64(freeBytes) / bytesPerGiB, float64(totalBytes) / bytesPerGiB, true
}

// openebsVGCapacity reads the LVM thin pool's size and data usage.
// vg_free/vg_size (the VG's unallocated space) was tried first but is wrong
// for a thin pool: prepareOpenEBSDevice allocates the pool as 95%VG up
// front, so vg_free reads ~5% free on a brand-new install regardless of
// actual usage. The pool LV's data_percent tracks real usage instead;
// lv_size (the 95%VG) is the real ceiling for VM data.
//
// The LVM LocalPV CSI driver has no capacity API, and the VG already lives
// on this host, so no cluster round-trip is needed. --units b sidesteps
// LVM's binary-vs-decimal GiB/GB ambiguity by asking for exact bytes
// (data_percent isn't a size field, so --units doesn't touch it).
func openebsVGCapacity() (freeGiB, totalGiB float64, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), StatusCheckTimeout)
	defer cancel()
	out, err := exec.RunNonInteractive(ctx, storage.LvsBin, "--noheadings", "--units", "b", "--nosuffix", "-o", "lv_size,data_percent", storage.OpenebsVGName+"/"+storage.OpenebsThinPoolLV).Output()
	if err != nil {
		return 0, 0, false
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		return 0, 0, false
	}
	sizeBytes, err1 := strconv.ParseFloat(fields[0], 64)
	dataPercent, err2 := strconv.ParseFloat(fields[1], 64)
	if err1 != nil || err2 != nil || sizeBytes == 0 {
		return 0, 0, false
	}
	totalGiB = sizeBytes / bytesPerGiB
	freeGiB = totalGiB * (1 - dataPercent/100)
	return freeGiB, totalGiB, true
}

func readyState(ready bool) string {
	if ready {
		return "ready"
	}
	return "not ready"
}
