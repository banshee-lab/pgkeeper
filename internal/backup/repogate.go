package backup

import (
	"context"
	"slices"
	"sync"
)

// repoGate serialises the restic phase of backups that share a repository.
// Waiters are served in FIFO order, so backups queue instead of failing on the
// repository lock or racing on repository initialisation. The zero value is
// ready to use.
type repoGate struct {
	mu    sync.Mutex
	repos map[string]*gateState
}

type gateState struct {
	holder  string
	waiters []*gateWaiter
}

type gateWaiter struct {
	owner string
	ready chan struct{} // closed, under repoGate.mu, when the gate is handed over
}

// acquire blocks until repo is free or ctx is done. onWait, if not nil, is
// called once (without locks held) when the gate is busy, with the current
// holder and the number of runs ahead (holder included). The returned release
// is idempotent.
func (g *repoGate) acquire(ctx context.Context, repo, owner string, onWait func(holder string, ahead int)) (func(), error) {
	g.mu.Lock()
	if g.repos == nil {
		g.repos = map[string]*gateState{}
	}
	st, busy := g.repos[repo]
	if !busy {
		g.repos[repo] = &gateState{holder: owner}
		g.mu.Unlock()
		return g.releaser(repo), nil
	}
	w := &gateWaiter{owner: owner, ready: make(chan struct{})}
	st.waiters = append(st.waiters, w)
	holder, ahead := st.holder, len(st.waiters) // holder + earlier waiters
	g.mu.Unlock()

	if onWait != nil {
		onWait(holder, ahead)
	}
	select {
	case <-w.ready:
		return g.releaser(repo), nil
	case <-ctx.Done():
		g.mu.Lock()
		select {
		case <-w.ready: // handed over just now: pass it on
			g.mu.Unlock()
			g.release(repo)
		default:
			st.waiters = slices.DeleteFunc(st.waiters, func(x *gateWaiter) bool { return x == w })
			g.mu.Unlock()
		}
		return nil, ctx.Err()
	}
}

func (g *repoGate) releaser(repo string) func() {
	var once sync.Once
	return func() { once.Do(func() { g.release(repo) }) }
}

func (g *repoGate) release(repo string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	st := g.repos[repo]
	if len(st.waiters) == 0 {
		delete(g.repos, repo)
		return
	}
	next := st.waiters[0]
	st.waiters = st.waiters[1:]
	st.holder = next.owner
	close(next.ready)
}
