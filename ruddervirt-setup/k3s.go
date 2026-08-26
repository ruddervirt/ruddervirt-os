// SPDX-License-Identifier: GPL-3.0-only

package main

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

	tea "github.com/charmbracelet/bubbletea"
)

var k3sVersionPattern = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)(?:-rc(\d+))?\+k3s(\d+)$`)

type parsedK3sVersion struct {
	major, minor, patch, rc, build int
	hasRC                          bool
}

func parseK3sVersion(v string) (parsedK3sVersion, bool) {
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

// compareK3sVersions returns >0 if a > b, 0 if equal, <0 if a < b (ok is
// false if either doesn't match k3s's vMAJOR.MINOR.PATCH[-rcN]+k3sBUILD
// tag format, e.g. an unexpected release naming scheme). A release
// candidate always sorts below the final release of the same version.
func compareK3sVersions(a, b string) (int, bool) {
	pa, ok := parseK3sVersion(a)
	if !ok {
		return 0, false
	}
	pb, ok := parseK3sVersion(b)
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

// fetchK3sVersions lists k3s release tags from GitHub, newest first, for
// the Settings screen's version picker.
func fetchK3sVersions() ([]string, error) {
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
		if p, ok := parseK3sVersion(r.TagName); ok && p.hasRC {
			continue
		}
		versions = append(versions, r.TagName)
	}
	if len(versions) == 0 {
		return nil, fmt.Errorf("no k3s releases found")
	}

	// GitHub returns releases in publish-date order, which interleaves
	// k3s's parallel minor-version branches (1.34.x, 1.35.x, 1.36.x, ...)
	// rather than sorting by version number - re-sort newest-first so the
	// picker reads in a sane order.
	sort.SliceStable(versions, func(i, j int) bool {
		cmp, ok := compareK3sVersions(versions[i], versions[j])
		if !ok {
			return false
		}
		return cmp > 0
	})

	return versions, nil
}

// resolveK3sVersion returns cfg's configured k3s version, or
// defaultK3sVersion if unset - shared by installK3sStep and planK3sInstall
// so the two can never disagree on what "desired" means.
func resolveK3sVersion(cfg Config) string {
	v := strings.TrimSpace(cfg.Versions.K3s)
	if v == "" {
		return defaultK3sVersion
	}
	return v
}

// installedK3sVersionPattern pulls the version token out of `k3s
// --version`'s first line, e.g. "k3s version v1.34.5+k3s1 (abcdef12)".
var installedK3sVersionPattern = regexp.MustCompile(`k3s version (\S+)`)

// parseInstalledK3sVersionOutput extracts and validates the version token
// from k3s --version's output - split out from installedK3sVersion so it's
// unit-testable without shelling out.
func parseInstalledK3sVersionOutput(out string) (string, bool) {
	m := installedK3sVersionPattern.FindStringSubmatch(out)
	if m == nil {
		return "", false
	}
	if _, ok := parseK3sVersion(m[1]); !ok {
		return "", false
	}
	return m[1], true
}

// installedK3sVersion reports the version of the k3s binary currently at
// /usr/local/bin/k3s, if any. Deliberately does NOT use runPrivileged -
// reading version info needs no root, and this may run before the operator
// has confirmed "yes" on Apply, so it must never risk a sudo prompt
// fighting with bubbletea's raw-terminal mode. ok is false if the binary
// is missing, fails to run, hangs past the timeout, or its output doesn't
// parse - callers treat that as "can't confirm it's already right."
func installedK3sVersion() (string, bool) {
	const binPath = "/usr/local/bin/k3s"
	if _, err := os.Stat(binPath); err != nil {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := DefaultRunner.Output(ctx, binPath, "--version")
	if err != nil {
		return "", false
	}
	return parseInstalledK3sVersionOutput(string(out))
}

// planK3sInstall previews installK3sStep: an exact match on the resolved
// version string, since in steady state the configured version is exactly
// what should already be on disk.
func planK3sInstall(cfg Config) string {
	version := resolveK3sVersion(cfg)
	if installed, ok := installedK3sVersion(); ok && installed == version {
		return fmt.Sprintf("skip - k3s %s already installed", version)
	}
	return fmt.Sprintf("will download k3s %s", version)
}

// installK3sStep downloads the k3s binary version configured in settings
// and overwrites /usr/local/bin/k3s with it, unless that version is
// already installed (see installedK3sVersion) - re-running install/update
// re-checks every time, so a matching binary is never redundantly
// re-fetched, but any mismatch (including an unparseable/missing install)
// is always corrected by a fresh download.
func installK3sStep(cfg Config, ch chan<- tea.Msg) {
	const label = "Installing k3s"
	const destPath = "/usr/local/bin/k3s"

	version := resolveK3sVersion(cfg)

	if installed, ok := installedK3sVersion(); ok && installed == version {
		ch <- stepOutputMsg(fmt.Sprintf("k3s %s already installed", version))
		ch <- stepDoneMsg{label: label}
		return
	}

	ch <- stepOutputMsg(fmt.Sprintf("Downloading k3s %s...", version))
	url := fmt.Sprintf(
		"https://github.com/k3s-io/k3s/releases/download/%s/k3s",
		strings.ReplaceAll(version, "+", "%2B"),
	)

	if err := downloadToPrivilegedPath(url, destPath, 0755); err != nil {
		ch <- stepDoneMsg{label: label, err: err}
		return
	}

	ch <- stepOutputMsg(fmt.Sprintf("k3s %s installed successfully", version))
	ch <- stepDoneMsg{label: label}
}

func downloadFile(url, destPath string) error {
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

// downloadToPrivilegedPath downloads url to a local tmp file, chmods it to
// mode, then moves it into destPath via runPrivileged - for destinations
// under a root-owned directory (e.g. /usr/local/bin, /etc/ruddervirt) that
// the TUI's own user can't write to directly. Safe as long as installSteps
// run strictly sequentially (they do - see launchStep/Update's stepDoneMsg
// handling, which only starts the next step once the current one reports
// done) and destPath basenames never collide across concurrent calls; if
// either changes, switch to os.CreateTemp for a collision-proof tmp name.
func downloadToPrivilegedPath(url, destPath string, mode os.FileMode) error {
	tmpPath := filepath.Join(os.TempDir(), filepath.Base(destPath))
	if err := downloadFile(url, tmpPath); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		return err
	}
	// mkdir -p first, same as writePrivileged (config.go) - destPath's
	// parent (/etc/ruddervirt/manifests/ for this func's only caller) isn't
	// guaranteed to exist otherwise; nothing provisions it via Ignition (see
	// server.bu).
	if out, err := runPrivileged("/usr/bin/mkdir", "-p", filepath.Dir(destPath)).CombinedOutput(); err != nil {
		return wrapCmdErr(out, err)
	}
	if out, err := runPrivileged("/usr/bin/mv", tmpPath, destPath).CombinedOutput(); err != nil {
		return wrapCmdErr(out, err)
	}
	return nil
}

// pollUntil emits a single "<msg>..." line, then silently retries check up to
// attempts times (sleeping interval between each) until it returns true.
func pollUntil(ch chan<- tea.Msg, msg string, attempts int, interval time.Duration, check func() bool) error {
	ch <- stepOutputMsg(msg + "...")
	for i := 0; i < attempts; i++ {
		if check() {
			return nil
		}
		time.Sleep(interval)
	}
	return fmt.Errorf("timed out %s", msg)
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

// deriveClusterDNS returns svcCIDR's network address + 10 (matching k3s's
// convention of putting cluster-dns at .10 within the service CIDR) - the
// Go equivalent of what render-k3s-config.sh derived via string-splitting,
// but a real CIDR parse instead of that script's "must be exactly /16"
// assumption.
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

// renderK3sConfigStep is the Go port of the former
// scripts/render-k3s-config.sh: it substitutes cfg's network values into
// the /etc/ruddervirt/k3s-config.yaml template (see server.bu) and writes
// the result to /etc/rancher/k3s/config.yaml, the file k3s reads at
// startup - same read-template/substitute/write shape as applyKubeOvn,
// just writing a plain file instead of applying a manifest.
func renderK3sConfigStep(cfg Config, ch chan<- tea.Msg) {
	const label = "Rendering k3s config"
	const templatePath = "/etc/ruddervirt/k3s-config.yaml"
	const outputPath = "/etc/rancher/k3s/config.yaml"

	data, err := os.ReadFile(templatePath)
	if err != nil {
		ch <- stepDoneMsg{label: label, err: err}
		return
	}

	clusterDNS, err := deriveClusterDNS(cfg.Network.SvcCIDR)
	if err != nil {
		ch <- stepDoneMsg{label: label, err: err}
		return
	}
	// The template's bind-address/advertise-address/node-ip/tls-san all
	// resolve to this, so an unresolvable local IP must fail the step
	// rather than render those fields blank.
	localIP, err := resolveLocalIP(cfg.Network)
	if err != nil {
		ch <- stepDoneMsg{label: label, err: fmt.Errorf("resolving local IP for k3s config: %w", err)}
		return
	}

	rendered := string(data)
	rendered = strings.ReplaceAll(rendered, "__POD_CIDR__", cfg.Network.PodCIDR)
	rendered = strings.ReplaceAll(rendered, "__SVC_CIDR__", cfg.Network.SvcCIDR)
	rendered = strings.ReplaceAll(rendered, "__CLUSTER_DNS__", clusterDNS)
	rendered = strings.ReplaceAll(rendered, "__LOCAL_IP__", localIP)

	ch <- stepOutputMsg(fmt.Sprintf("Writing %s...", outputPath))
	ch <- stepDoneMsg{label: label, err: writePrivileged(outputPath, []byte(rendered))}
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
// kube-ovn.yaml pins networking.stack to IPv4, so a dual-stack node reporting
// both an IPv4 and an IPv6 InternalIP must only contribute the IPv4 one, or
// KUBE_NODE_IPS ends up with a mixed-family list ovs-ovn/ic-controller can't
// parse. The selector is also used to apply kube-ovn's required master label.
func discoverMasterNodeIPs(kubectlBin string) ([]string, string, error) {
	for _, label := range []string{"node-role.kubernetes.io/control-plane", "node-role.kubernetes.io/master"} {
		out, err := runPrivileged(kubectlBin, "get", "nodes", "-l", label, "-o", "json").Output()
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

// applyKubeOvn renders manifests/kube-ovn.yaml's placeholders (see that
// file's header comment) and applies the result.
func applyKubeOvn(ch chan<- tea.Msg, kubectlBin, podCIDR, podGateway, svcCIDR string, masterIPs []string) error {
	const templatePath = "/etc/ruddervirt/manifests/kube-ovn.yaml"
	if err := writeManifestFile(ch, "kube-ovn.yaml"); err != nil {
		return err
	}
	data, err := os.ReadFile(templatePath)
	if err != nil {
		return err
	}

	quoted := make([]string, len(masterIPs))
	for i, ip := range masterIPs {
		quoted[i] = fmt.Sprintf("%q", ip)
	}

	rendered := string(data)
	rendered = strings.ReplaceAll(rendered, "__POD_CIDR__", podCIDR)
	rendered = strings.ReplaceAll(rendered, "__POD_GATEWAY__", podGateway)
	rendered = strings.ReplaceAll(rendered, "__SVC_CIDR__", svcCIDR)
	rendered = strings.ReplaceAll(rendered, "__MASTER_NODES__", strings.Join(quoted, ", "))

	tmp, err := os.CreateTemp("", "kube-ovn-*.yaml")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.WriteString(rendered); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	return runStreamed(ch, kubectlBin, "apply", "-f", tmpPath)
}

// kubeOvnCoreWorkloads are kube-ovn-v2's load-bearing components - the
// actual dataplane (ovs-ovn), the CNI plugin kubelet invokes for every pod
// sandbox (kube-ovn-cni), the controller that syncs K8s state into OVN
// (kube-ovn-controller), and the northbound/southbound DB + northd
// (ovn-central). kube-ovn-pinger is a diagnostic side-car, not load-bearing,
// so it's deliberately not waited on here. The chart's helper labels
// (templates/_helpers.tpl "kubeovn.labels") don't include a shared
// app.kubernetes.io/instance selector, and a namespace-wide pod wait would
// also catch the helm-controller's own bootstrap Job pod (which finishes
// and never reports Ready) - so each workload is waited on individually by
// kind/name instead.
var kubeOvnCoreWorkloads = []struct{ kind, name string }{
	{"daemonset", "ovs-ovn"},
	{"daemonset", "kube-ovn-cni"},
	{"deployment", "kube-ovn-controller"},
	{"deployment", "ovn-central"},
}

// waitForKubeOvnHealthy blocks until kube-ovn's core workloads have finished
// rolling out, so nothing that depends on pod networking (storage engines,
// KubeVirt/CDI, Aileron - all applied in prepareK3sStep after this) even
// attempts to apply against a half-up CNI.
func waitForKubeOvnHealthy(ch chan<- tea.Msg, kubectlBin string) error {
	if err := pollUntil(ch, "Waiting for kube-ovn to be installed", 60, 5*time.Second, func() bool {
		return runPrivileged(kubectlBin, "-n", "kube-system", "get", "daemonset/ovs-ovn").Run() == nil
	}); err != nil {
		return err
	}

	for _, w := range kubeOvnCoreWorkloads {
		ch <- stepOutputMsg(fmt.Sprintf("Waiting for %s/%s to become Ready...", w.kind, w.name))
		if err := runStreamed(ch, kubectlBin, "-n", "kube-system", "rollout", "status", w.kind+"/"+w.name, "--timeout=300s"); err != nil {
			return err
		}
	}
	return nil
}

// planApplyKubeOvn deliberately never touches a live cluster, same
// reasoning as planApplyManifests below.
func planApplyKubeOvn(cfg Config) string {
	return "will run - applies kube-ovn CNI and waits for it to become healthy"
}

// applyKubeOvnStep waits for the k3s API and a control-plane node, then
// applies and verifies kube-ovn - its own install phase (rather than folded
// into prepareK3sStep) so a broken CNI install fails loudly right here,
// before prepareK3sStep applies anything that needs working pod networking.
func applyKubeOvnStep(cfg Config, ch chan<- tea.Msg) {
	const label = "Applying kube-ovn"
	const kubectlBin = "/usr/local/bin/kubectl"
	const kubeconfig = "/etc/rancher/k3s/k3s.yaml"

	fail := func(err error) { ch <- stepDoneMsg{label: label, err: err} }

	if _, err := os.Stat(kubeconfig); err != nil {
		fail(fmt.Errorf("kubeconfig not found at %s: %w", kubeconfig, err))
		return
	}

	if err := pollUntil(ch, "Waiting for k3s API to become ready", 60, 2*time.Second, func() bool {
		return runPrivileged(kubectlBin, "get", "--raw=/readyz").Run() == nil
	}); err != nil {
		fail(err)
		return
	}

	var masterIPs []string
	var masterNodeSelector string
	if err := pollUntil(ch, "Waiting for a control-plane node", 60, 1*time.Second, func() bool {
		ips, selector, err := discoverMasterNodeIPs(kubectlBin)
		if err != nil {
			return false
		}
		masterIPs = ips
		masterNodeSelector = selector
		return true
	}); err != nil {
		fail(err)
		return
	}

	ch <- stepOutputMsg("Labeling kube-ovn master nodes...")
	if err := runStreamed(ch, kubectlBin, "label", "nodes", "-l", masterNodeSelector, "kube-ovn/role=master", "--overwrite"); err != nil {
		fail(err)
		return
	}

	podGateway, err := derivePodGateway(cfg.Network.PodCIDR)
	if err != nil {
		fail(err)
		return
	}

	ch <- stepOutputMsg("Applying kube-ovn...")
	if err := applyKubeOvn(ch, kubectlBin, cfg.Network.PodCIDR, podGateway, cfg.Network.SvcCIDR, masterIPs); err != nil {
		fail(err)
		return
	}

	if err := waitForKubeOvnHealthy(ch, kubectlBin); err != nil {
		fail(err)
		return
	}

	ch <- stepDoneMsg{label: label}
}

// prepareK3sStep is the Go port of the former scripts/prepare-k3s.sh: it
// applies the storage layer, waits for storage to become ready, then
// applies the remaining solution manifests. kube-ovn itself is applied and
// verified healthy earlier, by applyKubeOvnStep.
func prepareK3sStep(cfg Config, ch chan<- tea.Msg) {
	const label = "Applying manifests"
	const kubectlBin = "/usr/local/bin/kubectl"
	const kubeconfig = "/etc/rancher/k3s/k3s.yaml"

	fail := func(err error) { ch <- stepDoneMsg{label: label, err: err} }

	if _, err := os.Stat(kubeconfig); err != nil {
		fail(fmt.Errorf("kubeconfig not found at %s: %w", kubeconfig, err))
		return
	}

	// The CSI VolumeSnapshot CRDs/controller are shared infrastructure -
	// every engine's VolumeSnapshotClass (applied below, after the engine
	// itself is ready) depends on them being present.
	if err := writeManifestFile(ch, "snapshot-controller.yaml"); err != nil {
		fail(err)
		return
	}
	ch <- stepOutputMsg("Applying snapshot-controller...")
	if err := runStreamed(ch, kubectlBin, "apply", "-f", "/etc/ruddervirt/manifests/snapshot-controller.yaml"); err != nil {
		fail(err)
		return
	}

	if err := applyStorageEngine(ch, kubectlBin, cfg.Storage.Engine); err != nil {
		fail(err)
		return
	}

	snapshotClassManifest, err := storageSnapshotClassManifest(cfg.Storage.Engine)
	if err != nil {
		fail(err)
		return
	}

	for _, component := range kubevirtCDIInstallSpecs {
		ch <- stepOutputMsg(fmt.Sprintf("Applying %s operator and CRDs...", component.displayName))
		if err := runStreamed(ch, kubectlBin, "apply", "-f", "/etc/ruddervirt/manifests/"+component.operatorManifest); err != nil {
			fail(err)
			return
		}

		ch <- stepOutputMsg(fmt.Sprintf("Waiting for CRD %s to become Established...", component.crdName))
		if err := runStreamed(ch, kubectlBin, "wait", "--for=condition=Established", "crd/"+component.crdName, "--timeout=300s"); err != nil {
			fail(err)
			return
		}

		ch <- stepOutputMsg(fmt.Sprintf("Applying %s custom resource...", component.displayName))
		if err := runStreamed(ch, kubectlBin, "apply", "-f", "/etc/ruddervirt/manifests/"+component.customResourceManifest); err != nil {
			fail(err)
			return
		}
	}

	// openebs's StorageProfile override (manifests/openebs/base/storage-profile.yaml)
	// needs the StorageProfile CRD - but unlike kubevirts.kubevirt.io/
	// cdis.cdi.kubevirt.io above, that CRD isn't in cdi-operator.yaml's own
	// bundle: it only gets registered once the "cdi" custom resource just
	// applied is actually reconciled and CDI's operand (cdi-apiserver,
	// cdi-deployment, etc.) rolls out, which happens asynchronously after
	// this loop moves on. `kubectl wait` on a CRD name that doesn't exist
	// yet fails immediately with "not found" rather than polling for it to
	// appear, so - same as applyRookCeph's cephclusters.ceph.rook.io CRD
	// below - poll for the CRD to exist first, then wait on it.
	if cfg.Storage.Engine == "openebs" {
		if err := pollUntil(ch, "Waiting for CRD storageprofiles.cdi.kubevirt.io", 60, 5*time.Second, func() bool {
			return runPrivileged(kubectlBin, "get", "crd", "storageprofiles.cdi.kubevirt.io").Run() == nil
		}); err != nil {
			fail(err)
			return
		}
		if err := runStreamed(ch, kubectlBin, "wait", "--for=condition=Established", "crd/storageprofiles.cdi.kubevirt.io", "--timeout=60s"); err != nil {
			fail(err)
			return
		}
		ch <- stepOutputMsg("Applying openebs storage profile...")
		if err := runStreamed(ch, kubectlBin, "apply", "-f", "/etc/ruddervirt/manifests/openebs/base/storage-profile.yaml"); err != nil {
			fail(err)
			return
		}
	}

	// The rke2-multus chart applied just below doesn't bundle the
	// NetworkAttachmentDefinition CRD (confirmed against the chart tarball
	// from rke2-charts.rancher.io: no crds/ directory, no
	// CustomResourceDefinition template anywhere in it) - on real RKE2 it
	// ships as one of RKE2's own built-in static manifests, applied
	// separately from the chart, but k3s has no equivalent, so
	// ruddervirt-setup applies it directly. See manifests/multus-crd.yaml
	// for provenance. Aileron's chart applies its own
	// NetworkAttachmentDefinition ("egress-external"), so this must be
	// Established before applyAileron runs below, or its helm install fails
	// with "no matches for kind NetworkAttachmentDefinition".
	const multusCRD = "network-attachment-definitions.k8s.cni.cncf.io"
	if err := writeManifestFile(ch, "multus-crd.yaml"); err != nil {
		fail(err)
		return
	}
	ch <- stepOutputMsg("Applying multus CRDs...")
	if err := runStreamed(ch, kubectlBin, "apply", "-f", "/etc/ruddervirt/manifests/multus-crd.yaml"); err != nil {
		fail(err)
		return
	}
	if err := runStreamed(ch, kubectlBin, "wait", "--for=condition=Established", "crd/"+multusCRD, "--timeout=60s"); err != nil {
		fail(err)
		return
	}

	if err := writeManifestFile(ch, "multus.yaml"); err != nil {
		fail(err)
		return
	}
	ch <- stepOutputMsg("Applying multus...")
	if err := runStreamed(ch, kubectlBin, "apply", "-f", "/etc/ruddervirt/manifests/multus.yaml"); err != nil {
		fail(err)
		return
	}

	if err := runStreamed(ch, kubectlBin, "apply", "-f", "/etc/ruddervirt/manifests/"+snapshotClassManifest); err != nil {
		fail(err)
		return
	}

	// Defense-in-depth, same reasoning as prepareStorageDevice's own
	// re-check (storage.go): the Settings/Update screens' version field
	// already refuses to let the operator pick a new Aileron version once a
	// "stabilizer" HelmChart is on the cluster (see stabilizerLocked,
	// config.go), but that only guards the interactive picker. Without this
	// check here too, a hand-edited config - or simply hitting Apply with
	// whatever version was already saved before stabilizer showed up -
	// would still re-apply ruddervirt-setup's own "aileron" HelmChart every
	// run, fighting stabilizer for ownership of the same Aileron
	// installation.
	if stabilizerChartPresent() {
		ch <- stepOutputMsg("Aileron is managed by stabilizer - skipping")
	} else {
		aileronVersion := strings.TrimSpace(cfg.Versions.Aileron)
		if aileronVersion == "" {
			aileronVersion = defaultAileronVersion
		}
		ch <- stepOutputMsg("Applying aileron...")
		if err := applyAileron(ch, kubectlBin, aileronVersion, cfg.System.AileronUIEnabled); err != nil {
			fail(err)
			return
		}
	}

	if out, err := runPrivileged("/usr/bin/mkdir", "-p", "/var/lib/ruddervirt").CombinedOutput(); err != nil {
		fail(wrapCmdErr(out, err))
		return
	}
	if out, err := runPrivileged("/usr/bin/touch", "/var/lib/ruddervirt/prepare-k3s.done").CombinedOutput(); err != nil {
		fail(wrapCmdErr(out, err))
		return
	}

	ch <- stepDoneMsg{label: label}
}

// planApplyManifests deliberately never touches a live cluster - there may
// be no k3s API to query yet (e.g. the very first install, before any
// earlier step in this same run has even started k3s). kubectl apply/wait
// are both naturally idempotent (an already-applied resource or an
// already-Ready wait is a fast no-op), so there's no meaningful
// skip-vs-do distinction worth predicting here, unlike installK3sStep's or
// downloadKubeVirtCDIManifestsStep's on-disk version checks.
func planApplyManifests(cfg Config) string {
	return "will run - applies storage plus KubeVirt/CDI operators, CRDs, and custom resources, and Aileron unless a \"stabilizer\" HelmChart already manages it (already-applied resources and Ready waits are no-ops)"
}

// applyStorageEngine applies the kustomize overlay for engine and blocks
// until it's ready to provision volumes. Rook-Ceph gets its own wait (the
// CephCluster custom resource's Ready condition - Ceph's mons/OSDs come up
// in phases that a generic "all pods ready" check wouldn't capture
// correctly); Longhorn and OpenEBS share a simpler pattern: wait for their
// CSI driver to register, then for every pod in their namespace to be
// Ready.
func applyStorageEngine(ch chan<- tea.Msg, kubectlBin, engine string) error {
	// Refresh the on-disk manifests from this binary's embedded copy before
	// kubectl reads them below - see writeStorageManifests's doc comment for
	// why this, not Ignition, is what keeps an already-provisioned host's
	// storage engine upgradable.
	if err := writeStorageManifests(ch, engine); err != nil {
		return err
	}
	switch engine {
	case "rook-ceph":
		return applyRookCeph(ch, kubectlBin)
	case "longhorn":
		return applyGenericStorageEngine(ch, kubectlBin, "longhorn", "longhorn-system", "driver.longhorn.io")
	case "openebs":
		return applyGenericStorageEngine(ch, kubectlBin, "openebs", "openebs", "local.csi.openebs.io")
	default:
		return fmt.Errorf("unknown storage engine %q", engine)
	}
}

func applyRookCeph(ch chan<- tea.Msg, kubectlBin string) error {
	ch <- stepOutputMsg("Applying rook-ceph...")
	if err := runStreamed(ch, kubectlBin, "apply", "-k", "/etc/ruddervirt/manifests/rook-ceph/overlays/ruddervirt"); err != nil {
		return err
	}

	if err := pollUntil(ch, "Waiting for cephcluster CRD", 60, 5*time.Second, func() bool {
		return runPrivileged(kubectlBin, "get", "crd", "cephclusters.ceph.rook.io").Run() == nil
	}); err != nil {
		return err
	}

	if err := runStreamed(ch, kubectlBin, "wait", "--for=condition=Established", "crd/cephclusters.ceph.rook.io", "--timeout=300s"); err != nil {
		return err
	}

	if err := pollUntil(ch, "Waiting for cephcluster/rook-ceph", 60, 5*time.Second, func() bool {
		return runPrivileged(kubectlBin, "-n", "rook-ceph", "get", "cephcluster/rook-ceph").Run() == nil
	}); err != nil {
		return err
	}

	ch <- stepOutputMsg("Waiting for cephcluster/rook-ceph to become Ready (this can take a while)...")
	return runStreamed(ch, kubectlBin, "-n", "rook-ceph", "wait", "--for=condition=Ready", "cephcluster/rook-ceph", "--timeout=1800s")
}

// applyGenericStorageEngine applies manifestDir's kustomize overlay, then
// waits for csiDriver to register and every pod in namespace to become
// Ready - the readiness pattern shared by Longhorn and OpenEBS.
func applyGenericStorageEngine(ch chan<- tea.Msg, kubectlBin, manifestDir, namespace, csiDriver string) error {
	ch <- stepOutputMsg(fmt.Sprintf("Applying %s...", manifestDir))
	if err := runStreamed(ch, kubectlBin, "apply", "-k", "/etc/ruddervirt/manifests/"+manifestDir+"/overlays/ruddervirt"); err != nil {
		return err
	}

	if err := pollUntil(ch, fmt.Sprintf("Waiting for %s CSI driver to register", manifestDir), 120, 5*time.Second, func() bool {
		return runPrivileged(kubectlBin, "get", "csidriver", csiDriver).Run() == nil
	}); err != nil {
		return err
	}

	ch <- stepOutputMsg(fmt.Sprintf("Waiting for %s pods to become Ready (this can take a while)...", namespace))
	return runStreamed(ch, kubectlBin, "-n", namespace, "wait", "--for=condition=Ready", "pods", "--all", "--timeout=1800s")
}

// storageSnapshotClassManifest returns the engine-specific VolumeSnapshotClass
// manifest path (relative to /etc/ruddervirt/manifests), applied at the end
// of prepareK3sStep alongside kubevirt/cdi/multus.
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

// k3sVersionsFetchedMsg carries fetchK3sVersions' result back into Update -
// run as a tea.Cmd (not synchronously in initialModel) so a slow or
// unreachable network doesn't delay the TUI's first paint.
type k3sVersionsFetchedMsg struct {
	versions []string
}

func fetchK3sVersionsCmd() tea.Cmd {
	return func() tea.Msg {
		versions, _ := fetchK3sVersions() // best-effort - cycling just no-ops if this fails
		return k3sVersionsFetchedMsg{versions: versions}
	}
}
