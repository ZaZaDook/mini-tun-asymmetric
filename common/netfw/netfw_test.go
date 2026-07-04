package netfw

import (
	"fmt"
	"strings"
	"testing"
)

// fakeRunner records invocations and returns canned responses keyed by the
// command name, so we can assert which backend is picked and what it emits
// without touching a real firewall.
type fakeRunner struct {
	calls    []string
	respond  map[string]string // name → output for success
	failName map[string]bool   // name → return error
}

func (f *fakeRunner) run(name string, args ...string) (string, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	if f.failName[name] {
		return "", fmt.Errorf("%s not available", name)
	}
	return f.respond[name], nil
}

func TestDetectPrefersNft(t *testing.T) {
	f := &fakeRunner{respond: map[string]string{"nft": "nftables v1.0"}}
	fw := detect(f.run)
	if fw.Name() != "nft" {
		t.Fatalf("backend = %s, want nft", fw.Name())
	}
}

func TestDetectFirewalldWhenNoNft(t *testing.T) {
	f := &fakeRunner{
		failName: map[string]bool{"nft": true},
		respond:  map[string]string{"firewall-cmd": "running"},
	}
	fw := detect(f.run)
	if fw.Name() != "firewalld" {
		t.Fatalf("backend = %s, want firewalld", fw.Name())
	}
}

func TestDetectUfwWhenNoNftNoFirewalld(t *testing.T) {
	f := &fakeRunner{
		failName: map[string]bool{"nft": true, "firewall-cmd": true},
		respond:  map[string]string{"ufw": "Status: active"},
	}
	fw := detect(f.run)
	if fw.Name() != "ufw" {
		t.Fatalf("backend = %s, want ufw", fw.Name())
	}
}

func TestDetectNoopWhenNothing(t *testing.T) {
	f := &fakeRunner{failName: map[string]bool{"nft": true, "firewall-cmd": true, "ufw": true}}
	fw := detect(f.run)
	if fw.Name() != "noop" {
		t.Fatalf("backend = %s, want noop", fw.Name())
	}
}

// TestNftDynPortLifecycle verifies the nft set element add/del commands, and
// that they reference our table + set (so cleanup stays scoped to us).
func TestNftDynPortLifecycle(t *testing.T) {
	f := &fakeRunner{respond: map[string]string{"nft": ""}}
	n := &nftBackend{run: f.run}
	if err := n.AddDynPort(54321); err != nil {
		t.Fatal(err)
	}
	if err := n.DelDynPort(54321); err != nil {
		t.Fatal(err)
	}
	add, del := f.calls[0], f.calls[1]
	if !strings.Contains(add, "add element") || !strings.Contains(add, nftTable) ||
		!strings.Contains(add, nftDynSet) || !strings.Contains(add, "54321") {
		t.Fatalf("add element malformed: %s", add)
	}
	if !strings.Contains(del, "delete element") || !strings.Contains(del, "54321") {
		t.Fatalf("delete element malformed: %s", del)
	}
}

// TestNftReconcileDropsTable confirms reconcile targets ONLY our tagged table.
func TestNftReconcileDropsTable(t *testing.T) {
	f := &fakeRunner{respond: map[string]string{"nft": ""}}
	n := &nftBackend{run: f.run}
	if err := n.Reconcile(); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 || !strings.Contains(f.calls[0], "delete table inet "+nftTable) {
		t.Fatalf("reconcile should drop only our table, got: %v", f.calls)
	}
}

// TestNftEnsureBuildsTable checks the table/chain/set/rule are created and the
// covering rule is tagged.
func TestNftEnsureBuildsTable(t *testing.T) {
	f := &fakeRunner{respond: map[string]string{"nft": ""}}
	n := &nftBackend{run: f.run}
	if err := n.Ensure(); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(f.calls, "\n")
	for _, want := range []string{
		"add table inet " + nftTable,
		"add chain inet " + nftTable + " " + nftChain,
		"add set inet " + nftTable + " " + nftDynSet,
		"@" + nftDynSet + " accept",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("Ensure missing %q in:\n%s", want, joined)
		}
	}
}

// TestNftOpenPortTagged confirms a static control port is opened with our tag.
func TestNftOpenPortTagged(t *testing.T) {
	f := &fakeRunner{respond: map[string]string{"nft": ""}}
	n := &nftBackend{run: f.run}
	if err := n.OpenPort(UDP, 6881); err != nil {
		t.Fatal(err)
	}
	c := f.calls[0]
	if !strings.Contains(c, "udp dport 6881 accept") || !strings.Contains(c, Tag) {
		t.Fatalf("OpenPort malformed/untagged: %s", c)
	}
}
