// nftBackend manages our own nftables table `inet mini_tun_asymmetric`. Owning a
// dedicated table makes cleanup trivial and safe: `nft delete table` removes
// everything we made and nothing else. A named set `dyn_dports` holds the
// ephemeral data ports (port-hopping); one rule accepts the whole set, so adding
// or removing a port is a cheap set update, never a ruleset reload.
package netfw

import "fmt"

const (
	nftFamily = "inet"
	nftTable  = "mini_tun_asymmetric" // tagged table = unit of reconcile/teardown
	nftChain  = "input"
	nftDynSet = "dyn_dports" // dynamic UDP data ports (port-hopping)
)

type nftBackend struct {
	run runner
}

func (n *nftBackend) Name() string { return "nft" }

func (n *nftBackend) nft(args ...string) error {
	if out, err := n.run("nft", args...); err != nil {
		return fmt.Errorf("nft %v: %w (%s)", args, err, out)
	}
	return nil
}

// Reconcile drops our table if it lingered from a previous run. Deleting a
// non-existent table errors, so we ignore failure here — the goal is "ensure
// it's gone", and Ensure recreates a clean one.
func (n *nftBackend) Reconcile() error {
	_ = n.nft("delete", "table", nftFamily, nftTable) // ignore "No such file"
	return nil
}

// Ensure (re)creates our table, an input chain hooked at filter/input with a
// default policy of accept (we only ADD accepts; we never block the operator's
// traffic), and the dynamic-port set with a single covering rule.
func (n *nftBackend) Ensure() error {
	// Fresh start: drop then add (idempotent).
	_ = n.nft("delete", "table", nftFamily, nftTable)
	if err := n.nft("add", "table", nftFamily, nftTable); err != nil {
		return err
	}
	// input chain — hooked into the kernel input path. policy accept: we never
	// reduce connectivity, only document/track the ports we accept on.
	if err := n.nft("add", "chain", nftFamily, nftTable, nftChain,
		"{", "type", "filter", "hook", "input", "priority", "0", ";", "policy", "accept", ";", "}"); err != nil {
		return err
	}
	// named set of dynamic UDP data ports.
	if err := n.nft("add", "set", nftFamily, nftTable, nftDynSet,
		"{", "type", "inet_service", ";", "}"); err != nil {
		return err
	}
	// one rule covers the whole dynamic set.
	if err := n.nft("add", "rule", nftFamily, nftTable, nftChain,
		"udp", "dport", "@"+nftDynSet, "accept",
		"comment", quote(Tag+"-dyn")); err != nil {
		return err
	}
	return nil
}

func (n *nftBackend) Teardown() error {
	return n.nft("delete", "table", nftFamily, nftTable)
}

func (n *nftBackend) OpenPort(p Proto, port int) error {
	return n.nft("add", "rule", nftFamily, nftTable, nftChain,
		string(p), "dport", fmt.Sprint(port), "accept",
		"comment", quote(Tag+"-"+describePort(p, port)))
}

// ClosePort: nft can't delete a rule by content without its handle. For static
// control ports we rarely close at runtime; teardown/reconcile clears them via
// the whole-table drop. So ClosePort is best-effort: rebuild would be heavy, and
// the table drop already covers shutdown. We no-op with a clear contract.
func (n *nftBackend) ClosePort(p Proto, port int) error {
	// Intentionally a no-op for nft: static ports are torn down with the table.
	// (Closing a single static port mid-run isn't a current requirement.)
	return nil
}

func (n *nftBackend) AddDynPort(port int) error {
	return n.nft("add", "element", nftFamily, nftTable, nftDynSet, "{", fmt.Sprint(port), "}")
}

func (n *nftBackend) DelDynPort(port int) error {
	return n.nft("delete", "element", nftFamily, nftTable, nftDynSet, "{", fmt.Sprint(port), "}")
}

func quote(s string) string { return "\"" + s + "\"" }
