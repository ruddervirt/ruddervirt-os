package main

import "testing"

func TestPartitionSuffix(t *testing.T) {
	cases := []struct {
		disk string
		want string
	}{
		{"/dev/vda", ""},
		{"/dev/sda", ""},
		{"/dev/nvme0n1", "p"},
		{"/dev/mmcblk0", "p"},
	}
	for _, c := range cases {
		if got := partitionSuffix(c.disk); got != c.want {
			t.Errorf("partitionSuffix(%q) = %q, want %q", c.disk, got, c.want)
		}
	}
}
