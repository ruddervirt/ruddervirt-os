// SPDX-License-Identifier: GPL-3.0-only

// Package k3s holds k3s install/config logic and its post-CNI
// orchestration (storage/kubevirt/aileron, once kube-ovn is healthy).
//
// Exported functions take ch/wrap/write (and, for PrepareK3sStep,
// stabilizerPresent/applyAileronFn) as parameters instead of reaching for
// package main directly, since this package has no access to it - callers
// inject them, same pattern as internal/storage and internal/manifests.
// Package main keeps thin adapters matching installStep{run, plan} - see
// k3s_bridge.go.
package k3s

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"ruddervirt-setup/internal/exec"
	"ruddervirt-setup/internal/kubevirt"
	"ruddervirt-setup/internal/manifests"
	"ruddervirt-setup/internal/multus"
	"ruddervirt-setup/internal/network"
)

var k3sVersionPattern = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)(?:-rc(\d+))?\+k3s(\d+)$`)

type parsedK3sVersion struct {
	major, minor, patch, rc, build int
	hasRC                          bool
}

func ParseK3sVersion(v string) (parsedK3sVersion, bool) {
	m := k3sVersionPattern.FindStringSubmatch(v)
	if m == nil {
		return parsedK3sVersion{}, false
	}
	p := parsedK3sVersion{}
	p.major, _ = strconv.Atoi(m[1])
	p.minor, _ = strconv.Atoi(m[2])
	p.patch, _ = strconv.Atoi(m[3])
	if m[4] != "" {
		p.hasRC = true
		p.rc, _ = strconv.Atoi(m[4])
	}
	p.build, _ = strconv.Atoi(m[5])
	return p, true
}

// CompareK3sVersions returns >0 if a > b, 0 if equal, <0 if a < b (ok is
// false if either doesn't match k3s's vMAJOR.MINOR.PATCH[-rcN]+k3sBUILD tag
// format). A release candidate always sorts below its final release.
func CompareK3sVersions(a, b string) (int, bool) {
	pa, ok := ParseK3sVersion(a)
	if !ok {
		return 0, false
	}
	pb, ok := ParseK3sVersion(b)
	if !ok {
		return 0, false
	}
	if pa.major != pb.major {
		return pa.major - pb.major, true
	}
	if pa.minor != pb.minor {
		return pa.minor - pb.minor, true
	}
	if pa.patch != pb.patch {
		return pa.patch - pb.patch, true
	}
	if pa.hasRC != pb.hasRC {
		if pa.hasRC {
			return -1, true // a is a pre-release of b's final version
		}
		return 1, true // b is a pre-release of a's final version
	}
	if pa.hasRC && pa.rc != pb.rc {
		return pa.rc - pb.rc, true
	}
	if pa.build != pb.build {
		return pa.build - pb.build, true
	}
	return 0, true
}

// FetchK3sVersions lists k3s release tags from GitHub, newest first, for
// the Settings screen's version picker.
func FetchK3sVersions() ([]string, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/repos/k3s-io/k3s/releases", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "ruddervirt-setup")
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %s fetching k3s releases", resp.Status)
	}

	var releases []struct {
		TagName string `json:"tag_name"`
		Draft   bool   `json:"draft"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, err
	}

	var versions []string
	for _, r := range releases {
		if r.Draft || r.TagName == "" {
			continue
		}
		// k3s release candidates are never appropriate to offer here.
		if p, ok := ParseK3sVersion(r.TagName); ok && p.hasRC {
			continue
		}
		versions = append(versions, r.TagName)
	}
	if len(versions) == 0 {
		return nil, fmt.Errorf("no k3s releases found")
	}

	// GitHub returns releases in publish-date order, interleaving k3s's
	// parallel minor branches - re-sort newest-first for a sane picker order.
	sort.SliceStable(versions, func(i, j int) bool {
		cmp, ok := CompareK3sVersions(versions[i], versions[j])
		if !ok {
			return false
		}
		return cmp > 0
	})

	return versions, nil
}

// installedK3sVersionPattern pulls the version token out of `k3s
// --version`'s first line, e.g. "k3s version v1.34.5+k3s1 (abcdef12)".
var installedK3sVersionPattern = regexp.MustCompile(`k3s version (\S+)`)

// parseInstalledK3sVersionOutput extracts and validates the version token
// from k3s --version's output - split out from InstalledK3sVersion so it's
// unit-testable without shelling out.
func parseInstalledK3sVersionOutput(out string) (string, bool) {
	m := installedK3sVersionPattern.FindStringSubmatch(out)
	if m == nil {
		return "", false
	}
	if _, ok := ParseK3sVersion(m[1]); !ok {
		return "", false
	}
	return m[1], true
}

// InstalledK3sVersion reports the version of the k3s binary currently at
// /usr/local/bin/k3s, if any. Deliberately does NOT use RunPrivileged: this
// may run before the operator confirms Apply, so it must never risk a sudo
// prompt fighting bubbletea's raw-terminal mode. ok is false if the binary
// is missing, fails to run, times out, or its output doesn't parse -
// callers treat that as "can't confirm it's already right." Exported:
// package main's hostnameLocked (hostname.go) depends on this.
func InstalledK3sVersion() (string, bool) {
	const binPath = "/usr/local/bin/k3s"
	if _, err := os.Stat(binPath); err != nil {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.DefaultRunner.Output(ctx, binPath, "--version")
	if err != nil {
		return "", false
	}
	return parseInstalledK3sVersionOutput(string(out))
}

// InstallK3sStep downloads the configured k3s binary version and
// overwrites /usr/local/bin/k3s, unless that version is already installed
// (see InstalledK3sVersion). version is already resolved
// (configured-or-default) - see package main's installK3sStep adapter.
func InstallK3sStep(version string, ch chan<- exec.StepMsg, wrap func(line string) exec.StepMsg) error {
	const destPath = "/usr/local/bin/k3s"

	if installed, ok := InstalledK3sVersion(); ok && installed == version {
		ch <- wrap(fmt.Sprintf("k3s %s already installed", version))
		return nil
	}

	ch <- wrap(fmt.Sprintf("Downloading k3s %s...", version))
	url := fmt.Sprintf(
		"https://github.com/k3s-io/k3s/releases/download/%s/k3s",
		strings.ReplaceAll(version, "+", "%2B"),
	)

	if err := DownloadToPrivilegedPath(url, destPath, 0755); err != nil {
		return err
	}

	ch <- wrap(fmt.Sprintf("k3s %s installed successfully", version))
	return nil
}

// DownloadFile downloads url to destPath, overwriting it. Exported:
// package main's selfupdate flow (update.go) reuses this for its own
// checksum-verified download.
func DownloadFile(url, destPath string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %s downloading %s", resp.Status, url)
	}
	f, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

// DownloadToPrivilegedPath downloads url to a local tmp file, chmods it to
// mode, then moves it into destPath via exec.RunPrivileged, for
// destinations under a root-owned directory the TUI's user can't write to
// directly. Safe only because installSteps run strictly sequentially (see
// launchStep/Update's stepDoneMsg handling) and destPath basenames never
// collide across concurrent calls; switch to os.CreateTemp if either
// changes. Exported: package main's kubevirt_bridge.go passes this to
// internal/kubevirt.
func DownloadToPrivilegedPath(url, destPath string, mode os.FileMode) error {
	tmpPath := filepath.Join(os.TempDir(), filepath.Base(destPath))
	if err := DownloadFile(url, tmpPath); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		return err
	}
	// mkdir -p first: destPath's parent isn't guaranteed to exist (nothing
	// provisions it via Ignition - see server.bu), same as
	// internal/config.WritePrivileged.
	if out, err := exec.RunPrivileged("/usr/bin/mkdir", "-p", filepath.Dir(destPath)).CombinedOutput(); err != nil {
		return exec.WrapCmdErr(out, err)
	}
	if out, err := exec.RunPrivileged("/usr/bin/mv", tmpPath, destPath).CombinedOutput(); err != nil {
		return exec.WrapCmdErr(out, err)
	}
	return nil
}

// PollUntil emits a single "<msg>..." line (via wrap), then silently
// retries check (sleeping interval between attempts, up to attempts times)
// until it returns true. Exported: used directly by package main's
// stabilizer_settings_tui.go, and indirectly (via WaitForHelmInstallJob
// below) by internal/aileron and internal/stabilizer.
func PollUntil(ch chan<- exec.StepMsg, wrap func(line string) exec.StepMsg, msg string, attempts int, interval time.Duration, check func() bool) error {
	ch <- wrap(msg + "...")
	for i := 0; i < attempts; i++ {
		if check() {
			return nil
		}
		time.Sleep(interval)
	}
	return fmt.Errorf("timed out %s", msg)
}

// WaitForHelmInstallJob polls (via PollUntil, 60 attempts 5s apart) until
// jobNamespace/jobName exists, then waits up to 600s for condition=complete
// - shared, byte-identically, by internal/aileron's ApplyAileron and
// internal/stabilizer's applyStabilizer (internal/manifests.RenderAndApply
// is the render/apply half both also share). componentLabel names the
// component for the progress messages.
//
// Lives here, not internal/manifests, because it needs PollUntil, and
// internal/manifests importing internal/k3s back would be a cycle
// (internal/k3s already imports internal/manifests).
func WaitForHelmInstallJob(ch chan<- exec.StepMsg, wrap func(line string) exec.StepMsg, kubectlBin, jobNamespace, jobName, componentLabel string) error {
	if err := PollUntil(ch, wrap, fmt.Sprintf("Waiting for the %s helm-install job", componentLabel), 60, 5*time.Second, func() bool {
		return exec.RunPrivileged(kubectlBin, "-n", jobNamespace, "get", jobName).Run() == nil
	}); err != nil {
		return err
	}
	ch <- wrap(fmt.Sprintf("Waiting for the %s helm-install job to complete...", componentLabel))
	return exec.RunStreamed(ch, wrap, kubectlBin, "-n", jobNamespace, "wait", "--for=condition=complete", jobName, "--timeout=600s")
}

// derivePodGateway returns the first usable address in podCIDR (its network
// address + 1), matching what the old prepare-k3s.sh derived by hand.
func derivePodGateway(podCIDR string) (string, error) {
	ip, _, err := net.ParseCIDR(podCIDR)
	if err != nil {
		return "", fmt.Errorf("invalid pod CIDR %q: %w", podCIDR, err)
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return "", fmt.Errorf("pod CIDR must be IPv4: %s", podCIDR)
	}
	if ip4[3] >= 255 {
		return "", fmt.Errorf("unable to derive pod gateway from pod CIDR: %s", podCIDR)
	}
	return net.IPv4(ip4[0], ip4[1], ip4[2], ip4[3]+1).String(), nil
}

// deriveClusterDNS returns svcCIDR's network address + 10 (k3s's
// convention for cluster-dns), via a real CIDR parse rather than
// render-k3s-config.sh's "must be exactly /16" string-splitting.
func deriveClusterDNS(svcCIDR string) (string, error) {
	ip, _, err := net.ParseCIDR(svcCIDR)
	if err != nil {
		return "", fmt.Errorf("invalid service CIDR %q: %w", svcCIDR, err)
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return "", fmt.Errorf("service CIDR must be IPv4: %s", svcCIDR)
	}
	if ip4[3] > 245 {
		return "", fmt.Errorf("unable to derive cluster DNS from service CIDR: %s", svcCIDR)
	}
	return net.IPv4(ip4[0], ip4[1], ip4[2], ip4[3]+10).String(), nil
}

// RenderK3sConfigStep is the Go port of the former
// scripts/render-k3s-config.sh: substitutes net's values into the
// /etc/ruddervirt/k3s-config.yaml template (see server.bu) and writes
// /etc/rancher/k3s/config.yaml, the file k3s reads at startup. Takes
// network.NetworkConfig rather than the full Config, since that's all it
// ever used.
func RenderK3sConfigStep(netCfg network.NetworkConfig, ch chan<- exec.StepMsg, wrap func(line string) exec.StepMsg, write func(path string, data []byte) error) error {
	const templatePath = "/etc/ruddervirt/k3s-config.yaml"
	const outputPath = "/etc/rancher/k3s/config.yaml"

	data, err := os.ReadFile(templatePath)
	if err != nil {
		return err
	}

	clusterDNS, err := deriveClusterDNS(netCfg.SvcCIDR)
	if err != nil {
		return err
	}
	// bind-address/advertise-address/node-ip/tls-san all resolve to this,
	// so an unresolvable local IP must fail the step, not render blank.
	localIP, err := network.ResolveLocalIP(netCfg)
	if err != nil {
		return fmt.Errorf("resolving local IP for k3s config: %w", err)
	}

	rendered := string(data)
	rendered = strings.ReplaceAll(rendered, "__POD_CIDR__", netCfg.PodCIDR)
	rendered = strings.ReplaceAll(rendered, "__SVC_CIDR__", netCfg.SvcCIDR)
	rendered = strings.ReplaceAll(rendered, "__CLUSTER_DNS__", clusterDNS)
	rendered = strings.ReplaceAll(rendered, "__LOCAL_IP__", localIP)

	ch <- wrap(fmt.Sprintf("Writing %s...", outputPath))
	return write(outputPath, []byte(rendered))
}

type k8sNodeList struct {
	Items []struct {
		Status struct {
			Addresses []struct {
				Type    string `json:"type"`
				Address string `json:"address"`
			} `json:"addresses"`
		} `json:"status"`
	} `json:"items"`
}

// discoverMasterNodeIPs returns the IPv4 InternalIP of every control-plane
// (or, failing that, master-labeled) node and the selector that found them.
// Only IPv4 is kept: kube-ovn.yaml pins networking.stack to IPv4, and a
// mixed-family KUBE_NODE_IPS list breaks ovs-ovn/ic-controller. The
// selector is also used to apply kube-ovn's required master label.
func discoverMasterNodeIPs(kubectlBin string) ([]string, string, error) {
	for _, label := range []string{"node-role.kubernetes.io/control-plane", "node-role.kubernetes.io/master"} {
		out, err := exec.RunPrivileged(kubectlBin, "get", "nodes", "-l", label, "-o", "json").Output()
		if err != nil {
			continue
		}
		var list k8sNodeList
		if err := json.Unmarshal(out, &list); err != nil {
			continue
		}
		var ips []string
		for _, item := range list.Items {
			for _, addr := range item.Status.Addresses {
				if addr.Type != "InternalIP" {
					continue
				}
				if ip := net.ParseIP(addr.Address); ip != nil && ip.To4() != nil {
					ips = append(ips, addr.Address)
				}
			}
		}
		if len(ips) > 0 {
			return ips, label, nil
		}
	}
	return nil, "", fmt.Errorf("no control-plane node IPs found")
}

// applyKubeOvn renders manifests/kube-ovn.yaml's placeholders
// (__KUBE_OVN_VERSION__ comes from the Update screen's hand-curated
// supported-versions.yaml allowlist, same as KubeVirt/CDI, not a live
// fetch) and applies the resulting HelmChart object.
func applyKubeOvn(ch chan<- exec.StepMsg, wrap func(string) exec.StepMsg, write func(string, []byte) error, kubectlBin, version, podCIDR, podGateway, svcCIDR string, masterIPs []string) error {
	quoted := make([]string, len(masterIPs))
	for i, ip := range masterIPs {
		quoted[i] = fmt.Sprintf("%q", ip)
	}

	return manifests.RenderAndApply(ch, wrap, write, kubectlBin, "kube-ovn.yaml", "kube-ovn", []manifests.Placeholder{
		{Token: "__KUBE_OVN_VERSION__", Value: version},
		{Token: "__POD_CIDR__", Value: podCIDR},
		{Token: "__POD_GATEWAY__", Value: podGateway},
		{Token: "__SVC_CIDR__", Value: svcCIDR},
		{Token: "__MASTER_NODES__", Value: strings.Join(quoted, ", ")},
	})
}

// KubeOvnCoreWorkloads are kube-ovn-v2's load-bearing components: the
// dataplane (ovs-ovn), the CNI plugin (kube-ovn-cni), the K8s-to-OVN
// controller (kube-ovn-controller), and the OVN DB + northd (ovn-central).
// kube-ovn-pinger is a diagnostic side-car, deliberately not waited on.
// Waited on individually by kind/name: the chart's labels have no shared
// app.kubernetes.io/instance selector, and a namespace-wide wait would
// also catch helm-controller's bootstrap Job pod (which never reports
// Ready). Exported: status.go's home-screen service check ranges over this.
var KubeOvnCoreWorkloads = []struct{ Kind, Name string }{
	{"daemonset", "ovs-ovn"},
	{"daemonset", "kube-ovn-cni"},
	{"deployment", "kube-ovn-controller"},
	{"deployment", "ovn-central"},
}

// waitForKubeOvnHealthy blocks until kube-ovn's core workloads finish
// rolling out, so nothing depending on pod networking (storage, KubeVirt/
// CDI, Aileron - all applied in PrepareK3sStep after this) hits a half-up
// CNI.
func waitForKubeOvnHealthy(ch chan<- exec.StepMsg, wrap func(string) exec.StepMsg, kubectlBin string) error {
	if err := PollUntil(ch, wrap, "Waiting for kube-ovn to be installed", 60, 5*time.Second, func() bool {
		return exec.RunPrivileged(kubectlBin, "-n", "kube-system", "get", "daemonset/ovs-ovn").Run() == nil
	}); err != nil {
		return err
	}

	for _, w := range KubeOvnCoreWorkloads {
		ch <- wrap(fmt.Sprintf("Waiting for %s/%s to become Ready...", w.Kind, w.Name))
		if err := exec.RunStreamed(ch, wrap, kubectlBin, "-n", "kube-system", "rollout", "status", w.Kind+"/"+w.Name, "--timeout=300s"); err != nil {
			return err
		}
	}
	return nil
}

// ApplyKubeOvnStep waits for the k3s API and a control-plane node, then
// applies and verifies kube-ovn as its own phase (rather than folded into
// PrepareK3sStep), so a broken CNI install fails loudly here, before
// PrepareK3sStep applies anything needing working pod networking.
// podCIDR/svcCIDR/kubeOvnVersion are already resolved (configured-or-
// default) - see package main's applyKubeOvnStep adapter.
func ApplyKubeOvnStep(ch chan<- exec.StepMsg, wrap func(string) exec.StepMsg, write func(string, []byte) error, podCIDR, svcCIDR, kubeOvnVersion string) error {
	const kubectlBin = "/usr/local/bin/kubectl"
	const kubeconfig = "/etc/rancher/k3s/k3s.yaml"

	if _, err := os.Stat(kubeconfig); err != nil {
		return fmt.Errorf("kubeconfig not found at %s: %w", kubeconfig, err)
	}

	if err := PollUntil(ch, wrap, "Waiting for k3s API to become ready", 60, 2*time.Second, func() bool {
		return exec.RunPrivileged(kubectlBin, "get", "--raw=/readyz").Run() == nil
	}); err != nil {
		return err
	}

	var masterIPs []string
	var masterNodeSelector string
	if err := PollUntil(ch, wrap, "Waiting for a control-plane node", 60, 1*time.Second, func() bool {
		ips, selector, err := discoverMasterNodeIPs(kubectlBin)
		if err != nil {
			return false
		}
		masterIPs = ips
		masterNodeSelector = selector
		return true
	}); err != nil {
		return err
	}

	ch <- wrap("Labeling kube-ovn master nodes...")
	if err := exec.RunStreamed(ch, wrap, kubectlBin, "label", "nodes", "-l", masterNodeSelector, "kube-ovn/role=master", "--overwrite"); err != nil {
		return err
	}

	podGateway, err := derivePodGateway(podCIDR)
	if err != nil {
		return err
	}

	ch <- wrap("Applying kube-ovn...")
	if err := applyKubeOvn(ch, wrap, write, kubectlBin, kubeOvnVersion, podCIDR, podGateway, svcCIDR, masterIPs); err != nil {
		return err
	}

	return waitForKubeOvnHealthy(ch, wrap, kubectlBin)
}

// PrepareK3sStep is the Go port of the former scripts/prepare-k3s.sh: it
// applies the storage layer, waits for it to become ready, then applies the
// remaining solution manifests. kube-ovn itself is applied and verified
// healthy earlier, by ApplyKubeOvnStep.
//
// engine/aileronUIEnabled/aileronVersion/multusVersion/kubevirtVersion/
// cdiVersion are already resolved (configured-or-default).
// stabilizerPresent/applyAileronFn are injected (package main's
// stabilizerChartPresent/applyAileron, aileron_bridge.go) since this package
// has no access to package main - see package main's prepareK3sStep adapter.
func PrepareK3sStep(
	ch chan<- exec.StepMsg,
	wrap func(string) exec.StepMsg,
	write func(path string, data []byte) error,
	engine string,
	aileronUIEnabled bool,
	aileronVersion, multusVersion string,
	kubevirtVersion, cdiVersion string,
	stabilizerPresent func() bool,
	applyAileronFn func(ch chan<- exec.StepMsg, kubectlBin, version string, uiEnabled bool) error,
) error {
	const kubectlBin = "/usr/local/bin/kubectl"
	const kubeconfig = "/etc/rancher/k3s/k3s.yaml"

	if _, err := os.Stat(kubeconfig); err != nil {
		return fmt.Errorf("kubeconfig not found at %s: %w", kubeconfig, err)
	}

	// Captured before any KubeVirt CR (re)apply below, so the Aileron
	// restart trigger further down can tell "Aileron was already running
	// and might now be out of sync with the CR" apart from "Aileron is
	// about to be freshly installed this same run and will pick up the
	// current CR on its own first startup" - restarting in the latter case
	// would just be a pointless second ~30-90s rollout.
	aileronExistedBefore := exec.RunPrivileged(kubectlBin, "-n", "ruddervirt-system", "get", "deployment", "aileron").Run() == nil

	// The CSI VolumeSnapshot CRDs/controller are shared infrastructure -
	// every engine's VolumeSnapshotClass (applied below, after the engine
	// itself is ready) depends on them being present.
	if err := manifests.WriteManifestFile(ch, wrap, write, "snapshot-controller.yaml"); err != nil {
		return err
	}
	ch <- wrap("Applying snapshot-controller...")
	if err := exec.RunStreamed(ch, wrap, kubectlBin, "apply", "-f", "/etc/ruddervirt/manifests/snapshot-controller.yaml"); err != nil {
		return err
	}

	if err := applyStorageEngine(ch, wrap, write, kubectlBin, engine); err != nil {
		return err
	}

	snapshotClassManifest, err := storageSnapshotClassManifest(engine)
	if err != nil {
		return err
	}

	kubevirtCRChanged, err := applyKubeVirtCDIStep(ch, wrap, write, kubectlBin, kubevirtVersion, cdiVersion)
	if err != nil {
		return err
	}

	// openebs's StorageProfile override needs the StorageProfile CRD, but
	// unlike kubevirts.kubevirt.io/cdis.cdi.kubevirt.io above, it isn't in
	// cdi-operator.yaml's own bundle: it only registers once CDI's operand
	// actually rolls out, asynchronously, after this loop moves on.
	// `kubectl wait` on a nonexistent CRD fails immediately instead of
	// polling, so - same as applyRookCeph's cephclusters.ceph.rook.io CRD
	// below - poll for it to exist first, then wait on it.
	if engine == "openebs" {
		if err := PollUntil(ch, wrap, "Waiting for CRD storageprofiles.cdi.kubevirt.io", 60, 5*time.Second, func() bool {
			return exec.RunPrivileged(kubectlBin, "get", "crd", "storageprofiles.cdi.kubevirt.io").Run() == nil
		}); err != nil {
			return err
		}
		if err := exec.RunStreamed(ch, wrap, kubectlBin, "wait", "--for=condition=Established", "crd/storageprofiles.cdi.kubevirt.io", "--timeout=60s"); err != nil {
			return err
		}
		ch <- wrap("Applying openebs storage profile...")
		if err := exec.RunStreamed(ch, wrap, kubectlBin, "apply", "-f", "/etc/ruddervirt/manifests/openebs/base/storage-profile.yaml"); err != nil {
			return err
		}
	}

	// The rke2-multus chart applied below doesn't bundle the
	// NetworkAttachmentDefinition CRD (confirmed against the chart tarball:
	// no crds/ directory) - on real RKE2 it ships as a separate built-in
	// static manifest, but k3s has no equivalent, so ruddervirt-setup
	// applies it directly (see manifests/multus-crd.yaml for provenance).
	// Must be Established before applyAileronFn runs below, since Aileron's
	// chart applies its own NetworkAttachmentDefinition ("egress-external").
	const multusCRD = "network-attachment-definitions.k8s.cni.cncf.io"
	if err := manifests.WriteManifestFile(ch, wrap, write, "multus-crd.yaml"); err != nil {
		return err
	}
	ch <- wrap("Applying multus CRDs...")
	if err := exec.RunStreamed(ch, wrap, kubectlBin, "apply", "-f", "/etc/ruddervirt/manifests/multus-crd.yaml"); err != nil {
		return err
	}
	if err := exec.RunStreamed(ch, wrap, kubectlBin, "wait", "--for=condition=Established", "crd/"+multusCRD, "--timeout=60s"); err != nil {
		return err
	}

	ch <- wrap("Applying multus...")
	if err := multus.ApplyMultus(ch, wrap, write, kubectlBin, multusVersion); err != nil {
		return err
	}

	if err := exec.RunStreamed(ch, wrap, kubectlBin, "apply", "-f", "/etc/ruddervirt/manifests/"+snapshotClassManifest); err != nil {
		return err
	}

	// Defense-in-depth, same reasoning as internal/storage's own re-check:
	// the Settings/Update UI already refuses a new Aileron version once a
	// "stabilizer" HelmChart is present (see stabilizerLocked, config.go),
	// but that only guards the interactive picker. Without this check, a
	// hand-edited config would still re-apply ruddervirt-setup's own
	// "aileron" HelmChart, fighting stabilizer for ownership.
	if stabilizerPresent() {
		ch <- wrap("Aileron is managed by stabilizer - skipping")
	} else {
		ch <- wrap("Applying aileron...")
		if err := applyAileronFn(ch, kubectlBin, aileronVersion, aileronUIEnabled); err != nil {
			return err
		}
	}

	if err := restartAileronIfNeeded(ch, wrap, kubectlBin, kubevirtCRChanged, aileronExistedBefore); err != nil {
		return err
	}

	if out, err := exec.RunPrivileged("/usr/bin/mkdir", "-p", "/var/lib/ruddervirt").CombinedOutput(); err != nil {
		return exec.WrapCmdErr(out, err)
	}
	if out, err := exec.RunPrivileged("/usr/bin/touch", "/var/lib/ruddervirt/prepare-k3s.done").CombinedOutput(); err != nil {
		return exec.WrapCmdErr(out, err)
	}

	return nil
}

// applyKubeVirtCDIStep applies the KubeVirt/CDI operators, CRDs, and custom
// resources to the cluster, skipping any component whose desired version was
// already applied (kubevirt.KubeVirtClusterApplySatisfied/
// CDIClusterApplySatisfied), and marking a component applied
// (kubevirt.MarkKubeVirtClusterApplied/MarkCDIClusterApplied) once its own
// apply succeeds. Returns whether the KubeVirt component specifically was
// (re)applied - only its CR carries Aileron's feature-gate patches, so a
// CDI-only change never needs the Aileron restart PrepareK3sStep triggers
// off this.
func applyKubeVirtCDIStep(ch chan<- exec.StepMsg, wrap func(string) exec.StepMsg, write func(path string, data []byte) error, kubectlBin, kubevirtVersion, cdiVersion string) (bool, error) {
	kubevirtCRChanged := false
	for _, component := range kubevirt.CDIInstallSpecs {
		var desiredVersion string
		var satisfied bool
		switch component.DisplayName {
		case "KubeVirt":
			desiredVersion = kubevirtVersion
			satisfied = kubevirt.KubeVirtClusterApplySatisfied(desiredVersion)
		case "CDI":
			desiredVersion = cdiVersion
			satisfied = kubevirt.CDIClusterApplySatisfied(desiredVersion)
		}
		if satisfied {
			ch <- wrap(fmt.Sprintf("%s %s already applied to the cluster - skipping", component.DisplayName, desiredVersion))
			continue
		}

		ch <- wrap(fmt.Sprintf("Applying %s operator and CRDs...", component.DisplayName))
		if err := exec.RunStreamed(ch, wrap, kubectlBin, "apply", "-f", "/etc/ruddervirt/manifests/"+component.OperatorManifest); err != nil {
			return kubevirtCRChanged, err
		}

		ch <- wrap(fmt.Sprintf("Waiting for CRD %s to become Established...", component.CRDName))
		if err := exec.RunStreamed(ch, wrap, kubectlBin, "wait", "--for=condition=Established", "crd/"+component.CRDName, "--timeout=300s"); err != nil {
			return kubevirtCRChanged, err
		}

		ch <- wrap(fmt.Sprintf("Applying %s custom resource...", component.DisplayName))
		if err := exec.RunStreamed(ch, wrap, kubectlBin, "apply", "-f", "/etc/ruddervirt/manifests/"+component.CustomResourceManifest); err != nil {
			return kubevirtCRChanged, err
		}

		switch component.DisplayName {
		case "KubeVirt":
			if err := kubevirt.MarkKubeVirtClusterApplied(write, desiredVersion); err != nil {
				return kubevirtCRChanged, err
			}
			kubevirtCRChanged = true
		case "CDI":
			if err := kubevirt.MarkCDIClusterApplied(write, desiredVersion); err != nil {
				return kubevirtCRChanged, err
			}
		}
	}
	return kubevirtCRChanged, nil
}

// restartAileronIfNeeded restarts Aileron's Deployment after the KubeVirt CR
// was actually reapplied to a cluster where Aileron was already running -
// Aileron only patches its required feature gates/emulated machines onto the
// KubeVirt CR once, at its own controller-manager startup, and has no
// watch/reconcile loop that would otherwise notice the CR changing
// underneath it. Skipped when the KubeVirt CR wasn't actually reapplied this
// run, or when Aileron wasn't running yet beforehand - a fresh install's own
// first startup (applyAileronFn, called by PrepareK3sStep just before this)
// already covers the current CR, so restarting it again would just be a
// second, pointless rollout.
func restartAileronIfNeeded(ch chan<- exec.StepMsg, wrap func(string) exec.StepMsg, kubectlBin string, kubevirtCRChanged, aileronExistedBefore bool) error {
	if !kubevirtCRChanged || !aileronExistedBefore {
		return nil
	}
	ch <- wrap("Restarting aileron so it re-applies required KubeVirt feature gates...")
	return exec.RunStreamed(ch, wrap, kubectlBin, "-n", "ruddervirt-system", "rollout", "restart", "deployment/aileron")
}

// applyStorageEngine applies the kustomize overlay for engine and blocks
// until it's ready to provision volumes. Rook-Ceph gets its own wait
// (CephCluster's Ready condition, since Ceph's mons/OSDs come up in phases
// a generic "all pods ready" check wouldn't capture); Longhorn and OpenEBS
// share a simpler pattern: wait for their CSI driver to register, then for
// every pod in their namespace to be Ready.
func applyStorageEngine(ch chan<- exec.StepMsg, wrap func(string) exec.StepMsg, write func(string, []byte) error, kubectlBin, engine string) error {
	// Refresh on-disk manifests from the embedded copy before kubectl reads
	// them - see manifests.WriteStorageManifests's doc comment for why this,
	// not Ignition, keeps an already-provisioned host's engine upgradable.
	if err := manifests.WriteStorageManifests(ch, wrap, write, engine); err != nil {
		return err
	}
	switch engine {
	case "rook-ceph":
		return applyRookCeph(ch, wrap, kubectlBin)
	case "longhorn":
		return applyGenericStorageEngine(ch, wrap, kubectlBin, "longhorn", "longhorn-system", "driver.longhorn.io")
	case "openebs":
		return applyGenericStorageEngine(ch, wrap, kubectlBin, "openebs", "openebs", "local.csi.openebs.io")
	default:
		return fmt.Errorf("unknown storage engine %q", engine)
	}
}

func applyRookCeph(ch chan<- exec.StepMsg, wrap func(string) exec.StepMsg, kubectlBin string) error {
	ch <- wrap("Applying rook-ceph...")
	if err := exec.RunStreamed(ch, wrap, kubectlBin, "apply", "-k", "/etc/ruddervirt/manifests/rook-ceph/overlays/ruddervirt"); err != nil {
		return err
	}

	if err := PollUntil(ch, wrap, "Waiting for cephcluster CRD", 60, 5*time.Second, func() bool {
		return exec.RunPrivileged(kubectlBin, "get", "crd", "cephclusters.ceph.rook.io").Run() == nil
	}); err != nil {
		return err
	}

	if err := exec.RunStreamed(ch, wrap, kubectlBin, "wait", "--for=condition=Established", "crd/cephclusters.ceph.rook.io", "--timeout=300s"); err != nil {
		return err
	}

	if err := PollUntil(ch, wrap, "Waiting for cephcluster/rook-ceph", 60, 5*time.Second, func() bool {
		return exec.RunPrivileged(kubectlBin, "-n", "rook-ceph", "get", "cephcluster/rook-ceph").Run() == nil
	}); err != nil {
		return err
	}

	ch <- wrap("Waiting for cephcluster/rook-ceph to become Ready (this can take a while)...")
	return exec.RunStreamed(ch, wrap, kubectlBin, "-n", "rook-ceph", "wait", "--for=condition=Ready", "cephcluster/rook-ceph", "--timeout=1800s")
}

// applyGenericStorageEngine applies manifestDir's kustomize overlay, then
// waits for csiDriver to register and every pod in namespace to become
// Ready - the readiness pattern shared by Longhorn and OpenEBS.
func applyGenericStorageEngine(ch chan<- exec.StepMsg, wrap func(string) exec.StepMsg, kubectlBin, manifestDir, namespace, csiDriver string) error {
	ch <- wrap(fmt.Sprintf("Applying %s...", manifestDir))
	if err := exec.RunStreamed(ch, wrap, kubectlBin, "apply", "-k", "/etc/ruddervirt/manifests/"+manifestDir+"/overlays/ruddervirt"); err != nil {
		return err
	}

	if err := PollUntil(ch, wrap, fmt.Sprintf("Waiting for %s CSI driver to register", manifestDir), 120, 5*time.Second, func() bool {
		return exec.RunPrivileged(kubectlBin, "get", "csidriver", csiDriver).Run() == nil
	}); err != nil {
		return err
	}

	ch <- wrap(fmt.Sprintf("Waiting for %s pods to become Ready (this can take a while)...", namespace))
	return exec.RunStreamed(ch, wrap, kubectlBin, "-n", namespace, "wait", "--for=condition=Ready", "pods", "--all", "--timeout=1800s")
}

// storageSnapshotClassManifest returns the engine-specific VolumeSnapshotClass
// manifest path (relative to /etc/ruddervirt/manifests), applied at the end
// of PrepareK3sStep alongside kubevirt/cdi/multus.
func storageSnapshotClassManifest(engine string) (string, error) {
	switch engine {
	case "rook-ceph":
		return "rook-ceph/snapshot-class.yaml", nil
	case "longhorn":
		return "longhorn/snapshot-class.yaml", nil
	case "openebs":
		return "openebs/snapshot-class.yaml", nil
	default:
		return "", fmt.Errorf("unknown storage engine %q", engine)
	}
}
