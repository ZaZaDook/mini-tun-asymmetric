// Fallback firewall backends for hosts without nft: firewalld, ufw, and a no-op
// for hosts with no manageable firewall (e.g. filtered only by an external cloud
// security group). These lack nftables' named-set trick, so a dynamic data port
// is opened/closed as an individual rule. They still tag rules where the backend
// supports comments, so cleanup can target only ours.
package netfw

import "fmt"

// ── firewalld ──
type firewalldBackend struct{ run runner }

func (f *firewalldBackend) Name() string { return "firewalld" }

func (f *firewalldBackend) fw(args ...string) error {
	if out, err := f.run("firewall-cmd", args...); err != nil {
		return fmt.Errorf("firewall-cmd %v: %w (%s)", args, err, out)
	}
	return nil
}

// firewalld keeps rules across restarts (runtime + permanent). We don't persist
// dynamic data ports (they're per-session and short-lived) — only runtime. So a
// crash leaves no permanent dynamic leftovers; Reconcile is a no-op beyond that.
func (f *firewalldBackend) Reconcile() error { return nil }
func (f *firewalldBackend) Ensure() error    { return nil }
func (f *firewalldBackend) Teardown() error  { return nil }

func (f *firewalldBackend) OpenPort(p Proto, port int) error {
	// permanent so it survives reboots (control ports are long-lived), + runtime.
	if err := f.fw("--permanent", "--add-port", fmt.Sprintf("%d/%s", port, p)); err != nil {
		return err
	}
	if err := f.fw("--add-port", fmt.Sprintf("%d/%s", port, p)); err != nil {
		return err
	}
	return f.fw("--reload")
}

func (f *firewalldBackend) ClosePort(p Proto, port int) error {
	_ = f.fw("--permanent", "--remove-port", fmt.Sprintf("%d/%s", port, p))
	_ = f.fw("--remove-port", fmt.Sprintf("%d/%s", port, p))
	return f.fw("--reload")
}

// Dynamic data ports: runtime-only (no --permanent, no --reload) so they're
// cheap and vanish on restart. Still slower than nft sets but correct.
func (f *firewalldBackend) AddDynPort(port int) error {
	return f.fw("--add-port", fmt.Sprintf("%d/udp", port))
}
func (f *firewalldBackend) DelDynPort(port int) error {
	return f.fw("--remove-port", fmt.Sprintf("%d/udp", port))
}

// ── ufw ──
type ufwBackend struct{ run runner }

func (u *ufwBackend) Name() string { return "ufw" }

func (u *ufwBackend) ufw(args ...string) error {
	if out, err := u.run("ufw", args...); err != nil {
		return fmt.Errorf("ufw %v: %w (%s)", args, err, out)
	}
	return nil
}

func (u *ufwBackend) Reconcile() error { return nil }
func (u *ufwBackend) Ensure() error    { return nil }
func (u *ufwBackend) Teardown() error  { return nil }

func (u *ufwBackend) OpenPort(p Proto, port int) error {
	return u.ufw("allow", fmt.Sprintf("%d/%s", port, p))
}
func (u *ufwBackend) ClosePort(p Proto, port int) error {
	return u.ufw("delete", "allow", fmt.Sprintf("%d/%s", port, p))
}
func (u *ufwBackend) AddDynPort(port int) error {
	return u.ufw("allow", fmt.Sprintf("%d/udp", port))
}
func (u *ufwBackend) DelDynPort(port int) error {
	return u.ufw("delete", "allow", fmt.Sprintf("%d/udp", port))
}

// ── noop (no manageable firewall; relies on external/cloud filtering) ──
type noopBackend struct{}

func (n *noopBackend) Name() string                 { return "noop" }
func (n *noopBackend) Reconcile() error              { return nil }
func (n *noopBackend) Ensure() error                 { return nil }
func (n *noopBackend) Teardown() error               { return nil }
func (n *noopBackend) OpenPort(Proto, int) error     { return nil }
func (n *noopBackend) ClosePort(Proto, int) error    { return nil }
func (n *noopBackend) AddDynPort(int) error          { return nil }
func (n *noopBackend) DelDynPort(int) error          { return nil }
