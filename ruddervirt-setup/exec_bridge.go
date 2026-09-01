// SPDX-License-Identifier: GPL-3.0-only

package main

import "ruddervirt-setup/internal/installsteps"

// wrapStepOutput adapts a log line to stepOutputMsg for exec.RunStreamed's wrap param.
func wrapStepOutput(l string) installsteps.StepMsg { return stepOutputMsg(l) }
