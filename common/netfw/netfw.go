// Package netfw is the server-side firewall abstraction. It opens/closes UDP
// ports for the master/slave and, crucially, owns everything it creates under a
// single tagged namespace so a restart can reconcile (remove only OUR leftovers)
// without touching the operator's other rules.
//
// Design: nftables-first. On modern distros firewalld (RHEL/Fedora/SUSE), ufw
// (Debian/Ubuntu) and even "iptables" are all nftables underneath, so we create
// our OWN inet table `mini_tun_asymmetric`. That table is the unit of cleanup
// (drop the table = remove all our rules) and hosts a named set of dynamic data
// ports (port-hopping) we add/remove at runtime without reloading anything.
//
// Fallbacks: if `nft` is unavailable we fall back to firewalld (--add-port with
// our comment tag), then ufw, then a no-op (firewall disabled / filtered by an
// external cloud security group — we log a warning so the operator knows).
package netfw

import (
	"fmt"
	"os/exec"
	"strings"
)

// Tag marks every rule/table/comment we create, so reconcile/teardown can find
// and remove ONLY what belongs to us.
const Tag = "mini-tun-asymmetric"

// Proto is a transport protocol for a firewall rule.
type Proto string

const (
	UDP Proto = "udp"
	TCP Proto = "tcp"
)

// Firewall is the backend-agnostic interface the server uses. All methods are
// safe to call repeatedly (idempotent); a backend that can't perform an action
// returns an error rather than panicking.
type Firewall interface {
	// Name reports the backend in use (for logging): "nft" | "firewalld" | "ufw" | "noop".
	Name() string

	// Ensure brings our managed state into existence (e.g. the nft table + chains
	// + dynamic-port set). Called at startup AFTER Reconcile.
	Ensure() error

	// Reconcile removes any leftovers from a previous (possibly crashed) run —
	// only OUR tagged objects — then leaves a clean slate. Called first at startup.
	Reconcile() error

	// Teardown removes everything we created (graceful shutdown).
	Teardown() error

	// OpenPort statically allows inbound traffic to a fixed port (control ports).
	OpenPort(p Proto, port int) error
	// ClosePort removes a static allow added by OpenPort.
	ClosePort(p Proto, port int) error

	// AddDynPort adds an ephemeral UDP data port (port-hopping) to the dynamic
	// set — one rule covers the whole set, so this is a cheap set update, not a
	// ruleset reload. DelDynPort removes it when the session ends.
	AddDynPort(port int) error
	DelDynPort(port int) error
}

// runner executes a command and returns combined output + error. Abstracted so
// tests can inject a fake without shelling out.
type runner func(name string, args ...string) (string, error)

// Detect picks the best available backend on this host. Order: nft → firewalld
// → ufw → noop. The chosen backend is returned ready to use (call Reconcile then
// Ensure). detect uses the provided runner (real exec in production).
func detect(run runner) Firewall {
	// nft present and usable?
	if _, err := run("nft", "--version"); err == nil {
		return &nftBackend{run: run}
	}
	// firewalld running?
	if out, err := run("firewall-cmd", "--state"); err == nil && strings.Contains(out, "running") {
		return &firewalldBackend{run: run}
	}
	// ufw present?
	if _, err := run("ufw", "status"); err == nil {
		return &ufwBackend{run: run}
	}
	return &noopBackend{}
}

// describePort renders "udp/7000" for logs/comments.
func describePort(p Proto, port int) string {
	return fmt.Sprintf("%s/%d", p, port)
}

// execRun is the production runner: runs a command and returns combined output.
func execRun(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}

// New detects and returns the best firewall backend for this host using real
// command execution. Call Reconcile() then Ensure() before opening ports.
func New() Firewall {
	return detect(execRun)
}
