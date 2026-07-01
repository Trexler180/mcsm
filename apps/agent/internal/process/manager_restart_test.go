package process

import (
	"sync"
	"testing"
)

// TestRemoveInstanceIdentity covers the Restart race: if a concurrent Start
// replaced the instance after Restart stopped the old one, removeInstance must
// leave the fresh instance in place rather than orphaning it.
func TestRemoveInstanceIdentity(t *testing.T) {
	m := NewManager(t.TempDir())
	old := newInstance(m.serverRoot, "s1", StartConfig{})
	fresh := newInstance(m.serverRoot, "s1", StartConfig{})

	m.instances["s1"] = fresh
	m.removeInstance("s1", old) // stale reference: must be a no-op
	if m.instances["s1"] != fresh {
		t.Fatal("removeInstance with a stale instance removed the fresh one")
	}

	m.removeInstance("s1", fresh)
	if _, ok := m.instances["s1"]; ok {
		t.Fatal("removeInstance did not remove the matching instance")
	}

	m.removeInstance("s1", fresh) // absent id: must not panic
}

// TestRestartRefusesWhileProcessAlive ensures Restart fails instead of
// double-starting when the old process has not finalized after stop. The
// instance here has no real process (pid 0), so stop→kill returns nil without
// closing done — exactly the "kill timed out" shape.
func TestRestartRefusesWhileProcessAlive(t *testing.T) {
	m := NewManager(t.TempDir())
	inst := newInstance(m.serverRoot, "s1", StartConfig{})
	m.instances["s1"] = inst

	if err := m.Restart("s1"); err == nil {
		t.Fatal("Restart succeeded while the old instance never exited")
	}
	if m.instances["s1"] != inst {
		t.Fatal("Restart removed the instance despite refusing to restart")
	}
}

// TestManagerConcurrentAccess hammers the instance-map paths under -race.
func TestManagerConcurrentAccess(t *testing.T) {
	m := NewManager(t.TempDir())
	inst := newInstance(m.serverRoot, "s1", StartConfig{})
	m.instances["s1"] = inst

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for iter := 0; iter < 2000; iter++ {
				switch n % 4 {
				case 0:
					m.Status("s1")
				case 1:
					m.RegisterDir("s1", "dir")
				case 2:
					m.removeInstance("s1", inst)
					m.mu.Lock()
					m.instances["s1"] = inst
					m.mu.Unlock()
				case 3:
					m.GetDir("s1")
				}
			}
		}(i)
	}
	wg.Wait()
}
