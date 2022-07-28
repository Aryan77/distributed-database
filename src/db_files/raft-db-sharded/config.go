package raft_db_sharded

import (
	"dist-db/db_files/raft"
	shardctrler2 "dist-db/db_files/shard-controller"
	"dist-db/util_files/labrpc"
)
import "testing"
import "os"

// import "log"
import crand "crypto/rand"
import "math/big"
import "math/rand"
import "encoding/base64"
import "sync"
import "runtime"
import "strconv"
import "fmt"
import "time"

func randstring(n int) string {
	b := make([]byte, 2*n)
	crand.Read(b)
	s := base64.URLEncoding.EncodeToString(b)
	return s[0:n]
}

func makeSeed() int64 {
	max := big.NewInt(int64(1) << 62)
	bigx, _ := crand.Int(crand.Reader, max)
	x := bigx.Int64()
	return x
}

func random_handles(kvh []*labrpc.ClientEnd) []*labrpc.ClientEnd {
	sa := make([]*labrpc.ClientEnd, len(kvh))
	copy(sa, kvh)
	for i := range sa {
		j := rand.Intn(i + 1)
		sa[i], sa[j] = sa[j], sa[i]
	}
	return sa
}

type group struct {
	gid       int
	servers   []*ShardKV
	saved     []*raft.Persister
	endnames  [][]string
	mendnames [][]string
}

type config struct {
	mu    sync.Mutex
	t     *testing.T
	net   *labrpc.Network
	start time.Time

	nctrlers      int
	ctrlerservers []*shardctrler2.ShardCtrler
	mck           *shardctrler2.Clerk

	ngroups int
	n       int
	groups  []*group

	clerks       map[*Clerk][]string
	nextClientId int
	maxraftstate int
}

func (cfg *config) checkTimeout() {
	if !cfg.t.Failed() && time.Since(cfg.start) > 120*time.Second {
		cfg.t.Fatal("test took longer than 120 seconds")
	}
}

func (cfg *config) cleanup() {
	for gi := 0; gi < cfg.ngroups; gi++ {
		cfg.ShutdownGroup(gi)
	}
	for i := 0; i < cfg.nctrlers; i++ {
		cfg.ctrlerservers[i].Kill()
	}
	cfg.net.Cleanup()
	cfg.checkTimeout()
}

func (cfg *config) checklogs() {
	for gi := 0; gi < cfg.ngroups; gi++ {
		for i := 0; i < cfg.n; i++ {
			raft := cfg.groups[gi].saved[i].RaftStateSize()
			snap := len(cfg.groups[gi].saved[i].ReadSnapshot())
			if cfg.maxraftstate >= 0 && raft > 8*cfg.maxraftstate {
				cfg.t.Fatalf("persister.RaftStateSize() %v, but maxraftstate %v",
					raft, cfg.maxraftstate)
			}
			if cfg.maxraftstate < 0 && snap > 0 {
				cfg.t.Fatalf("maxraftstate is -1, but snapshot is non-empty!")
			}
		}
	}
}

func (cfg *config) ctrlername(i int) string {
	return "ctrler" + strconv.Itoa(i)
}

func (cfg *config) servername(gid int, i int) string {
	return "server-" + strconv.Itoa(gid) + "-" + strconv.Itoa(i)
}

func (cfg *config) makeClient() *Clerk {
	cfg.mu.Lock()
	defer cfg.mu.Unlock()

	ends := make([]*labrpc.ClientEnd, cfg.nctrlers)
	endnames := make([]string, cfg.n)
	for j := 0; j < cfg.nctrlers; j++ {
		endnames[j] = randstring(20)
		ends[j] = cfg.net.MakeEnd(endnames[j])
		cfg.net.Connect(endnames[j], cfg.ctrlername(j))
		cfg.net.Enable(endnames[j], true)
	}

	ck := MakeClerk(ends, func(servername string) *labrpc.ClientEnd {
		name := randstring(20)
		end := cfg.net.MakeEnd(name)
		cfg.net.Connect(name, servername)
		cfg.net.Enable(name, true)
		return end
	})
	cfg.clerks[ck] = endnames
	cfg.nextClientId++
	return ck
}

func (cfg *config) deleteClient(ck *Clerk) {
	cfg.mu.Lock()
	defer cfg.mu.Unlock()

	v := cfg.clerks[ck]
	for i := 0; i < len(v); i++ {
		os.Remove(v[i])
	}
	delete(cfg.clerks, ck)
}

func (cfg *config) ShutdownServer(gi int, i int) {
	cfg.mu.Lock()
	defer cfg.mu.Unlock()

	gg := cfg.groups[gi]

	for j := 0; j < len(gg.servers); j++ {
		name := gg.endnames[i][j]
		cfg.net.Enable(name, false)
	}
	for j := 0; j < len(gg.mendnames[i]); j++ {
		name := gg.mendnames[i][j]
		cfg.net.Enable(name, false)
	}

	cfg.net.DeleteServer(cfg.servername(gg.gid, i))

	if gg.saved[i] != nil {
		gg.saved[i] = gg.saved[i].Copy()
	}

	kv := gg.servers[i]
	if kv != nil {
		cfg.mu.Unlock()
		kv.Kill()
		cfg.mu.Lock()
		gg.servers[i] = nil
	}
}

func (cfg *config) ShutdownGroup(gi int) {
	for i := 0; i < cfg.n; i++ {
		cfg.ShutdownServer(gi, i)
	}
}

func (cfg *config) StartServer(gi int, i int) {
	cfg.mu.Lock()

	gg := cfg.groups[gi]

	gg.endnames[i] = make([]string, cfg.n)
	for j := 0; j < cfg.n; j++ {
		gg.endnames[i][j] = randstring(20)
	}

	ends := make([]*labrpc.ClientEnd, cfg.n)
	for j := 0; j < cfg.n; j++ {
		ends[j] = cfg.net.MakeEnd(gg.endnames[i][j])
		cfg.net.Connect(gg.endnames[i][j], cfg.servername(gg.gid, j))
		cfg.net.Enable(gg.endnames[i][j], true)
	}

	mends := make([]*labrpc.ClientEnd, cfg.nctrlers)
	gg.mendnames[i] = make([]string, cfg.nctrlers)
	for j := 0; j < cfg.nctrlers; j++ {
		gg.mendnames[i][j] = randstring(20)
		mends[j] = cfg.net.MakeEnd(gg.mendnames[i][j])
		cfg.net.Connect(gg.mendnames[i][j], cfg.ctrlername(j))
		cfg.net.Enable(gg.mendnames[i][j], true)
	}

	if gg.saved[i] != nil {
		gg.saved[i] = gg.saved[i].Copy()
	} else {
		gg.saved[i] = raft.MakePersister()
	}
	cfg.mu.Unlock()

	gg.servers[i] = StartServer(ends, i, gg.saved[i], cfg.maxraftstate,
		gg.gid, mends,
		func(servername string) *labrpc.ClientEnd {
			name := randstring(20)
			end := cfg.net.MakeEnd(name)
			cfg.net.Connect(name, servername)
			cfg.net.Enable(name, true)
			return end
		})

	kvsvc := labrpc.MakeService(gg.servers[i])
	rfsvc := labrpc.MakeService(gg.servers[i].rf)
	srv := labrpc.MakeServer()
	srv.AddService(kvsvc)
	srv.AddService(rfsvc)
	cfg.net.AddServer(cfg.servername(gg.gid, i), srv)
}

func (cfg *config) StartGroup(gi int) {
	for i := 0; i < cfg.n; i++ {
		cfg.StartServer(gi, i)
	}
}

func (cfg *config) StartCtrlerserver(i int) {
	ends := make([]*labrpc.ClientEnd, cfg.nctrlers)
	for j := 0; j < cfg.nctrlers; j++ {
		endname := randstring(20)
		ends[j] = cfg.net.MakeEnd(endname)
		cfg.net.Connect(endname, cfg.ctrlername(j))
		cfg.net.Enable(endname, true)
	}

	p := raft.MakePersister()

	cfg.ctrlerservers[i] = shardctrler2.StartServer(ends, i, p)

	msvc := labrpc.MakeService(cfg.ctrlerservers[i])
	rfsvc := labrpc.MakeService(cfg.ctrlerservers[i].Raft())
	srv := labrpc.MakeServer()
	srv.AddService(msvc)
	srv.AddService(rfsvc)
	cfg.net.AddServer(cfg.ctrlername(i), srv)
}

func (cfg *config) shardclerk() *shardctrler2.Clerk {
	ends := make([]*labrpc.ClientEnd, cfg.nctrlers)
	for j := 0; j < cfg.nctrlers; j++ {
		name := randstring(20)
		ends[j] = cfg.net.MakeEnd(name)
		cfg.net.Connect(name, cfg.ctrlername(j))
		cfg.net.Enable(name, true)
	}

	return shardctrler2.MakeClerk(ends)
}

func (cfg *config) join(gi int) {
	cfg.joinm([]int{gi})
}

func (cfg *config) joinm(gis []int) {
	m := make(map[int][]string, len(gis))
	for _, g := range gis {
		gid := cfg.groups[g].gid
		servernames := make([]string, cfg.n)
		for i := 0; i < cfg.n; i++ {
			servernames[i] = cfg.servername(gid, i)
		}
		m[gid] = servernames
	}
	cfg.mck.Join(m)
}

func (cfg *config) leave(gi int) {
	cfg.leavem([]int{gi})
}

func (cfg *config) leavem(gis []int) {
	gids := make([]int, 0, len(gis))
	for _, g := range gis {
		gids = append(gids, cfg.groups[g].gid)
	}
	cfg.mck.Leave(gids)
}

var ncpu_once sync.Once

func make_config(t *testing.T, n int, unreliable bool, maxraftstate int) *config {
	ncpu_once.Do(func() {
		if runtime.NumCPU() < 2 {
			fmt.Printf("warning: only one CPU, which may conceal locking bugs\n")
		}
		rand.Seed(makeSeed())
	})
	runtime.GOMAXPROCS(4)
	cfg := &config{}
	cfg.t = t
	cfg.maxraftstate = maxraftstate
	cfg.net = labrpc.MakeNetwork()
	cfg.start = time.Now()

	cfg.nctrlers = 3
	cfg.ctrlerservers = make([]*shardctrler2.ShardCtrler, cfg.nctrlers)
	for i := 0; i < cfg.nctrlers; i++ {
		cfg.StartCtrlerserver(i)
	}
	cfg.mck = cfg.shardclerk()

	cfg.ngroups = 3
	cfg.groups = make([]*group, cfg.ngroups)
	cfg.n = n
	for gi := 0; gi < cfg.ngroups; gi++ {
		gg := &group{}
		cfg.groups[gi] = gg
		gg.gid = 100 + gi
		gg.servers = make([]*ShardKV, cfg.n)
		gg.saved = make([]*raft.Persister, cfg.n)
		gg.endnames = make([][]string, cfg.n)
		gg.mendnames = make([][]string, cfg.nctrlers)
		for i := 0; i < cfg.n; i++ {
			cfg.StartServer(gi, i)
		}
	}

	cfg.clerks = make(map[*Clerk][]string)
	cfg.nextClientId = cfg.n + 1000
	cfg.net.Reliable(!unreliable)

	return cfg
}
