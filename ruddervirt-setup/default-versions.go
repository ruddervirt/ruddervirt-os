// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	_ "embed"
	"fmt"

	"gopkg.in/yaml.v3"
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

// defaultK3sVersion, defaultAileronVersion, defaultKubeVirtVersion,
// defaultCDIVersion, defaultStabilizerVersion, defaultKubeOVNVersion and
// defaultMultusVersion are populated from default-versions.yaml in init
// below - same "compiled-in, parsed once at startup" role the former
// hardcoded consts played, just editable without touching Go source.
var (
	defaultK3sVersion        string
	defaultAileronVersion    string
	defaultKubeVirtVersion   string
	defaultCDIVersion        string
	defaultStabilizerVersion string
	defaultKubeOVNVersion    string
	defaultMultusVersion     string
)

func init() {
	// This file is compiled in, not user input or a network fetch - a
	// parse failure or malformed version string here means the embedded
	// data itself is broken, which is a build-time bug that should fail
	// loudly rather than silently leave a default version blank.
	var dv defaultVersionsFile
	if err := yaml.Unmarshal(defaultVersionsYAML, &dv); err != nil {
		panic(fmt.Sprintf("ruddervirt-setup/default-versions.yaml failed to parse: %v", err))
	}

	if _, ok := parseK3sVersion(dv.K3s); !ok {
		panic(fmt.Sprintf("ruddervirt-setup/default-versions.yaml: k3s version %q doesn't match vMAJOR.MINOR.PATCH[-rcN]+k3sBUILD", dv.K3s))
	}
	for _, v := range []struct{ name, value string }{
		{"aileron", dv.Aileron},
		{"kubevirt", dv.KubeVirt},
		{"cdi", dv.CDI},
		{"stabilizer", dv.Stabilizer},
		{"kubeovn", dv.KubeOVN},
		{"multus", dv.Multus},
	} {
		if _, ok := parseSemver(v.value); !ok {
			panic(fmt.Sprintf("ruddervirt-setup/default-versions.yaml: %s version %q doesn't match vMAJOR.MINOR.PATCH", v.name, v.value))
		}
	}

	defaultK3sVersion = dv.K3s
	defaultAileronVersion = dv.Aileron
	defaultKubeVirtVersion = dv.KubeVirt
	defaultCDIVersion = dv.CDI
	defaultStabilizerVersion = dv.Stabilizer
	defaultKubeOVNVersion = dv.KubeOVN
	defaultMultusVersion = dv.Multus
}
