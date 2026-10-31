//go:build !windows

package proc

import (
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// Meter measures programs: their share of the processors and the memory they hold, from ps. The system does not tell
// what a program takes of the graphics processor, so that is left out.
type Meter struct{}

// NewMeter starts measuring; Close ends it.
func NewMeter() *Meter { return &Meter{} }

// Close releases what the meter holds.
func (m *Meter) Close() {}

// Sample measures each program in pids together with everything it started. A program that has ended is left out.
func (m *Meter) Sample(pids ...int) map[int]Usage {
	out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=,pcpu=,rss=").Output()
	if err != nil {
		return nil
	}
	type row struct {
		cpu float64
		rss uint64
	}
	rows := map[int]row{}
	children := map[int][]int{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) != 4 {
			continue
		}
		pid, _ := strconv.Atoi(f[0])
		ppid, _ := strconv.Atoi(f[1])
		cpu, _ := strconv.ParseFloat(f[2], 64)
		rss, _ := strconv.ParseUint(f[3], 10, 64)
		rows[pid] = row{cpu, rss << 10}
		children[ppid] = append(children[ppid], pid)
	}
	cpus := float64(runtime.NumCPU())
	res := map[int]Usage{}
	for _, root := range pids {
		if _, ok := rows[root]; !ok {
			continue
		}
		var u Usage
		queue := []int{root}
		for len(queue) > 0 {
			pid := queue[0]
			queue = append(queue[1:], children[pid]...)
			u.CPU += rows[pid].cpu / cpus
			u.Memory += rows[pid].rss
		}
		res[root] = u
	}
	return res
}
