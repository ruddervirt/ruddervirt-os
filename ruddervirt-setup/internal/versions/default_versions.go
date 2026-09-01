// SPDX-License-Identifier: GPL-3.0-only

package versions

import (
	_ "embed"
	"fmt"

	"gopkg.in/yaml.v3"
	"ruddervirt-setup/internal/k3s"
)

//go:embed default-versions.yaml
var defaultVersionsYAML []byte

// defaultVersionsFile is default-versions.yaml's shape - see that file for
// what these values mean and how to bump them.
type defaultVersionsFile struct {
	K3s        string `yaml:"k3s"`
	Aileron    string `yaml:"aileron"`
	KubeVirt   string `yaml:"kubevirt"`
	CDI        string `yaml:"cdi"`
	Stabilizer string `yaml:"stabilizer"`
	KubeOVN    string `yaml:"kubeovn"`
	Multus     string `yaml:"multus"`
}

// DefaultK3sVersion, DefaultAileronVersion, DefaultKubeVirtVersion,
// DefaultCDIVersion, DefaultStabilizerVersion, DefaultKubeOVNVersion and
// DefaultMultusVersion are populated from default-versions.yaml in init
// below. Exported: package main's k3s_bridge.go/kubevirt_bridge.go/app.go,
// plus internal/config's config.go and internal/stabilizer's stabilizer.go,
// all read these as fallbacks for an unset Config field.
var (
	DefaultK3sVersion        string
	DefaultAileronVersion    string
	DefaultKubeVirtVersion   string
	DefaultCDIVersion        string
	DefaultStabilizerVersion string
	DefaultKubeOVNVersion    string
	DefaultMultusVersion     string
)

func init() {
	// Compiled in, not user input - a parse failure means a build-time
	// bug, so fail loudly rather than leave a default version blank.
	var dv defaultVersionsFile
	if err := yaml.Unmarshal(defaultVersionsYAML, &dv); err != nil {
		panic(fmt.Sprintf("ruddervirt-setup/internal/versions/default-versions.yaml failed to parse: %v", err))
	}

	if _, ok := k3s.ParseK3sVersion(dv.K3s); !ok {
		panic(fmt.Sprintf("ruddervirt-setup/internal/versions/default-versions.yaml: k3s version %q doesn't match vMAJOR.MINOR.PATCH[-rcN]+k3sBUILD", dv.K3s))
	}
	for _, v := range []struct{ name, value string }{
		{"aileron", dv.Aileron},
		{"kubevirt", dv.KubeVirt},
		{"cdi", dv.CDI},
		{"stabilizer", dv.Stabilizer},
		{"kubeovn", dv.KubeOVN},
		{"multus", dv.Multus},
	} {
		if _, ok := ParseSemver(v.value); !ok {
			panic(fmt.Sprintf("ruddervirt-setup/internal/versions/default-versions.yaml: %s version %q doesn't match vMAJOR.MINOR.PATCH", v.name, v.value))
		}
	}

	DefaultK3sVersion = dv.K3s
	DefaultAileronVersion = dv.Aileron
	DefaultKubeVirtVersion = dv.KubeVirt
	DefaultCDIVersion = dv.CDI
	DefaultStabilizerVersion = dv.Stabilizer
	DefaultKubeOVNVersion = dv.KubeOVN
	DefaultMultusVersion = dv.Multus
}
