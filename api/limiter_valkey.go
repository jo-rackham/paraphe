package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/valkey-io/valkey-go"
)

// Valkey holds the rate-limit counters when several instances must share one
// view — with three replicas, per-process ceilings would be three ceilings.
//
// It is a COUNTER store, not a datastore: keys are HMAC bucket names, values
// are small integers, every one carries a TTL of minutes, and losing all of
// them (a failover, a restart) merely re-opens the windows. Hence no
// persistence anywhere in the deployment files, and hence `noeviction` on
// the server: under memory pressure a write FAILS — taking the loud,
// per-instance degraded path (limiter.go) — rather than silently evicting
// someone else's window, which would weaken the ceilings with no trace.
//
// Sentinel is how it stays available: the client asks the sentinels for the
// master and follows their +switch-master announcements. valkey-go's
// sentinel tracking is event-driven; TopologyRefreshInterval adds the
// periodic reconciliation its own documentation recommends, because a single
// missed event would otherwise bind this client to a demoted master until
// restart.

const (
	// valkeyTimeout bounds one counter round-trip. The limiter sits in
	// front of sign-in: during a failover it must degrade in well under a
	// second, not hold every attempt for a connect timeout.
	valkeyTimeout = 500 * time.Millisecond

	sentinelScheme = "valkey+sentinel://"
	directScheme   = "valkey://"
)

// countScript: one atomic round-trip — count the event, arm the window on
// its first event, answer the total and the time left. The defensive PTTL
// branch re-arms a key that somehow lost its expiry: a counter that never
// resets would refuse its subject for ever.
const countScript = `
local n = redis.call('INCR', KEYS[1])
if n == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
elseif redis.call('PTTL', KEYS[1]) < 0 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
return {n, redis.call('PTTL', KEYS[1])}`

type valkeyStore struct {
	option valkey.ClientOption
	script *valkey.Lua

	// The client is built LAZILY, because valkey-go dials on construction:
	// built eagerly, a Valkey outage at startup would fail the start — the
	// one moment the degraded posture must hold exactly like any other.
	// Once built, valkey-go reconnects on its own; until then, redials are
	// gated to one attempt a second so a down Valkey costs each request an
	// instant error, not a dial.
	mu      sync.Mutex
	cli     valkey.Client
	nextTry time.Time
}

// errValkeyDown: the gate is closed — someone dialed and failed within the
// last second. The caller degrades; nothing waits.
var errValkeyDown = fmt.Errorf("valkey unreachable (redialing at most once a second)")

const redialEvery = time.Second

// newValkeyStore prepares a store for valkey_url:
//
//	valkey://host:6379                                   one server
//	valkey+sentinel://h1:26379,h2:26379,h3:26379/name    a sentinel group
//
// The password comes from valkey_password, never from the URL: the URL ends
// up in process listings and deployment files, and the same password serves
// the data nodes and the sentinels (the deployment files configure both).
// Only the URL itself can be refused here; reachability is the degraded
// path's business, at startup as at any other time.
func newValkeyStore(rawURL, password string) (*valkeyStore, error) {
	option, err := valkeyOption(rawURL, password)
	if err != nil {
		return nil, err
	}
	return &valkeyStore{
		option: option,
		script: valkey.NewLuaScript(countScript),
	}, nil
}

// client returns the live client, building it on the first call — and after
// an outage, at most once a second, OUTSIDE the lock: the losers of the gate
// fail fast into the degraded path instead of queueing behind a dial.
func (v *valkeyStore) client() (valkey.Client, error) {
	v.mu.Lock()
	if v.cli != nil {
		cli := v.cli
		v.mu.Unlock()
		return cli, nil
	}
	if time.Now().Before(v.nextTry) {
		v.mu.Unlock()
		return nil, errValkeyDown
	}
	v.nextTry = time.Now().Add(redialEvery)
	v.mu.Unlock()

	cli, err := valkey.NewClient(v.option)
	if err != nil {
		return nil, fmt.Errorf("connecting to Valkey: %w", err)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.cli != nil {
		// another dial won while ours ran: keep the first, drop the spare
		cli.Close()
		return v.cli, nil
	}
	v.cli = cli
	return cli, nil
}

func valkeyOption(rawURL, password string) (valkey.ClientOption, error) {
	if strings.Contains(rawURL, "@") {
		return valkey.ClientOption{}, fmt.Errorf(
			"valkey_url must not carry credentials (%q): set valkey_password",
			rawURL)
	}
	option := valkey.ClientOption{
		Password:     password,
		ClientName:   "paraphe",
		Dialer:       net.Dialer{Timeout: valkeyTimeout},
		DisableCache: true,
	}
	switch {
	case strings.HasPrefix(rawURL, sentinelScheme):
		rest := strings.TrimPrefix(rawURL, sentinelScheme)
		hosts, master, ok := strings.Cut(rest, "/")
		if !ok || master == "" || hosts == "" {
			return valkey.ClientOption{}, fmt.Errorf(
				"valkey_url %q: expected %sh1:26379,h2:26379/master-name",
				rawURL, sentinelScheme)
		}
		addrs, err := splitAddrs(hosts, "26379")
		if err != nil {
			return valkey.ClientOption{}, fmt.Errorf("valkey_url %q: %w", rawURL, err)
		}
		option.InitAddress = addrs
		option.Sentinel = valkey.SentinelOption{
			MasterSet: master,
			Password:  password,
			// see the file comment: event-driven alone, a missed
			// +switch-master sticks to the demoted master until restart
			TopologyRefreshInterval: 5 * time.Second,
		}
		return option, nil
	case strings.HasPrefix(rawURL, directScheme):
		rest := strings.TrimPrefix(rawURL, directScheme)
		if rest == "" || strings.Contains(rest, "/") {
			return valkey.ClientOption{}, fmt.Errorf(
				"valkey_url %q: expected %shost:6379", rawURL, directScheme)
		}
		addrs, err := splitAddrs(rest, "6379")
		if err != nil {
			return valkey.ClientOption{}, fmt.Errorf("valkey_url %q: %w", rawURL, err)
		}
		option.InitAddress = addrs
		return option, nil
	}
	return valkey.ClientOption{}, fmt.Errorf(
		"valkey_url %q: expected a %s or %s address", rawURL,
		directScheme, sentinelScheme)
}

func splitAddrs(csv, defaultPort string) ([]string, error) {
	var addrs []string
	for _, h := range strings.Split(csv, ",") {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		if _, _, err := net.SplitHostPort(h); err != nil {
			// a bare host takes the conventional port; anything else
			// (two colons, garbage) is refused with the parser's reason
			bare := h + ":" + defaultPort
			if _, _, err := net.SplitHostPort(bare); err != nil {
				return nil, fmt.Errorf("unreadable address %q: %w", h, err)
			}
			h = bare
		}
		addrs = append(addrs, h)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("no address given")
	}
	return addrs, nil
}

// ping says at startup whether Valkey answers — an operator who just typed a
// wrong URL or password hears it immediately, in one line, instead of
// finding the degraded-mode error later. The start does NOT fail on it: the
// runtime posture is to degrade out loud, and startup is no different.
func (v *valkeyStore) ping(ctx context.Context) error {
	cli, err := v.client()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, valkeyTimeout)
	defer cancel()
	return cli.Do(ctx, cli.B().Ping().Build()).Error()
}

func (v *valkeyStore) count(ctx context.Context, key string, window time.Duration) (int64, time.Duration, error) {
	cli, err := v.client()
	if err != nil {
		return 0, 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, valkeyTimeout)
	defer cancel()
	res, err := v.script.Exec(ctx, cli,
		[]string{key}, []string{strconv.FormatInt(window.Milliseconds(), 10)}).AsIntSlice()
	if err != nil {
		return 0, 0, fmt.Errorf("counting in Valkey: %w", err)
	}
	if len(res) != 2 {
		return 0, 0, fmt.Errorf("counting in Valkey: %d values instead of 2", len(res))
	}
	total, ttl := res[0], res[1]
	if ttl < 0 {
		// the script re-arms lost expiries, so this is unreachable; if it
		// ever answers, the full window is the safe reading
		ttl = window.Milliseconds()
	}
	return total, time.Duration(ttl) * time.Millisecond, nil
}

func (v *valkeyStore) forget(ctx context.Context, key string) error {
	cli, err := v.client()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, valkeyTimeout)
	defer cancel()
	if err := cli.Do(ctx, cli.B().Del().Key(key).Build()).Error(); err != nil {
		return fmt.Errorf("clearing a Valkey counter: %w", err)
	}
	return nil
}

func (v *valkeyStore) close() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.cli != nil {
		v.cli.Close()
		v.cli = nil
	}
}

// startupLog: one line saying which mode the limiter runs in — the absence
// of a shared store is a MODE (a single replica needs none), never a
// silence.
func limiterStartupLog(shared *valkeyStore, url string) {
	if shared == nil {
		slog.Info("rate limits held in process memory: exact for one " +
			"instance; set valkey_url when running several")
		return
	}
	mode := "direct"
	if strings.HasPrefix(url, sentinelScheme) {
		mode = "sentinel"
	}
	slog.Info("rate limits shared through Valkey", "mode", mode)
}
