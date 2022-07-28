package raft_db

import (
	"bytes"
	raft2 "dist-db/db_files/raft"
	"dist-db/util_files/labgob"
	"dist-db/util_files/labrpc"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const Padding = "    "

func (kv *KVServer) Debug(format string, a ...interface{}) {
	preamble := strings.Repeat(Padding, kv.me)
	epilogue := strings.Repeat(Padding, kv.serversLen-kv.me-1)
	prefix := fmt.Sprintf("%s%s S%d %s[SERVER] ", preamble, raft2.Microseconds(time.Now()), kv.me, epilogue)
	format = prefix + format
	log.Print(fmt.Sprintf(format, a...))
}

type KVServer struct {
	mu      sync.Mutex
	me      int
	rf      *raft2.Raft
	applyCh chan raft2.ApplyMsg
	dead    int32

	maxraftstate int
	serversLen   int

	kv          map[string]string
	dedup       map[int32]interface{}
	get         map[int]chan string
	done        map[int]chan struct{}
	lastApplied int
}

func (kv *KVServer) readSnapshot(snapshot []byte) {
	var dedup map[int32]interface{}
	var kvmap map[string]string
	r := bytes.NewBuffer(snapshot)
	d := labgob.NewDecoder(r)
	if e := d.Decode(&dedup); e != nil {
		dedup = make(map[int32]interface{})
	}
	if e := d.Decode(&kvmap); e != nil {
		kvmap = make(map[string]string)
	}
	kv.dedup = dedup
	kv.kv = kvmap
}

func (kv *KVServer) DoApply() {
	for v := range kv.applyCh {
		if kv.killed() {
			return
		}

		if v.CommandValid {
			kv.apply(v)
			if _, isLeader := kv.rf.GetState(); !isLeader {
				continue
			}
			kv.mu.Lock()
			switch args := v.Command.(type) {
			case GetArgs:
				getCh := kv.get[v.CommandIndex]
				val := ""
				if s, ok := kv.kv[args.Key]; ok {
					val = s
				}
				kv.mu.Unlock()
				go func() {
					getCh <- val
				}()
				break
			case PutAppendArgs:
				putCh := kv.done[v.CommandIndex]
				kv.mu.Unlock()
				go func() {
					putCh <- struct{}{}
				}()
			}
		} else {
			b := kv.rf.CondInstallSnapshot(v.SnapshotTerm, v.SnapshotIndex, v.Snapshot)
			kv.Debug("CondInstallSnapshot %t SnapshotTerm=%d SnapshotIndex=%d len(Snapshot)=%d", b, v.SnapshotTerm, v.SnapshotIndex, len(v.Snapshot))
			if b {
				kv.lastApplied = v.SnapshotIndex
				kv.readSnapshot(v.Snapshot)
			}
		}
	}
}

func (kv *KVServer) apply(v raft2.ApplyMsg) {
	if v.CommandIndex <= kv.lastApplied {
		kv.Debug("reject ApplyMsg due to smaller Index. lastApplied=%d v=%+v", kv.lastApplied, v)
		return
	}
	var key string
	switch args := v.Command.(type) {
	case GetArgs:
		key = args.Key
		kv.lastApplied = v.CommandIndex
		break
	case PutAppendArgs:
		key = args.Key
		if dup, ok := kv.dedup[args.ClientId]; ok {
			if putDup, ok := dup.(PutAppendArgs); ok && putDup.RequestId == args.RequestId {
				kv.Debug("duplicate found for putDup=%+v  args=%+v", putDup, args)
				break
			}
		}
		if args.Type == PutOp {
			kv.kv[args.Key] = args.Value
		} else {
			kv.kv[args.Key] += args.Value
		}
		kv.dedup[args.ClientId] = v.Command
		kv.lastApplied = v.CommandIndex
	}
	kv.Debug("applied {%d %+v} value: %s", v.CommandIndex, v.Command, kv.kv[key])
	if kv.rf.GetStateSize() >= kv.maxraftstate && kv.maxraftstate != -1 {
		kv.Debug("checkSnapshot: kv.rf.GetStateSize(%d) >= kv.maxraftstate(%d)", kv.rf.GetStateSize(), kv.maxraftstate)
		w := new(bytes.Buffer)
		e := labgob.NewEncoder(w)
		if err := e.Encode(kv.dedup); err != nil {
			panic(err)
		}
		if err := e.Encode(kv.kv); err != nil {
			panic(err)
		}
		kv.rf.Snapshot(v.CommandIndex, w.Bytes())
	}
}

const TimeoutInterval = 500 * time.Millisecond

func (kv *KVServer) Get(args *GetArgs, reply *GetReply) {
	if _, isLeader := kv.rf.GetState(); !isLeader {
		reply.Err = ErrWrongLeader
		return
	}
	op := *args
	i, _, isLeader := kv.rf.Start(op)
	kv.Debug("raft start Get i=%d %+v", i, op)
	if !isLeader {
		reply.Err = ErrWrongLeader
		return
	}
	ch := make(chan string, 1)
	kv.mu.Lock()
	kv.get[i] = ch
	kv.mu.Unlock()
	select {
	case v := <-ch:
		kv.Debug("raft Get done: %+v => %+v", op, v)
		reply.Value = v
		reply.Err = OK
		return
	case <-time.After(TimeoutInterval):
		reply.Err = ErrTimeout
		return
	}
}

func (kv *KVServer) PutAppend(args *PutAppendArgs, reply *PutAppendReply) {
	if _, isLeader := kv.rf.GetState(); !isLeader {
		reply.Err = ErrWrongLeader
		return
	}
	op := *args
	i, _, isLeader := kv.rf.Start(op)
	kv.Debug("raft start Put i=%d %+v", i, op)
	if !isLeader {
		reply.Err = ErrWrongLeader
		return
	}
	ch := make(chan struct{}, 1)
	kv.mu.Lock()
	kv.done[i] = ch
	kv.mu.Unlock()
	select {
	case <-ch:
		kv.Debug("raft Put done: %+v", op)
		reply.Err = OK
		return
	case <-time.After(TimeoutInterval):
		reply.Err = ErrTimeout
		return
	}
}

func (kv *KVServer) Kill() {
	atomic.StoreInt32(&kv.dead, 1)
	kv.rf.Kill()
}

func (kv *KVServer) killed() bool {
	z := atomic.LoadInt32(&kv.dead)
	return z == 1
}

func StartKVServer(servers []*labrpc.ClientEnd, me int, persister *raft2.Persister, maxraftstate int) *KVServer {
	labgob.Register(GetArgs{})
	labgob.Register(PutAppendArgs{})

	kv := new(KVServer)
	kv.me = me
	kv.maxraftstate = maxraftstate
	kv.serversLen = len(servers)

	kv.applyCh = make(chan raft2.ApplyMsg)
	kv.rf = raft2.Make(servers, me, persister, kv.applyCh)
	kv.get = make(map[int]chan string)
	kv.done = make(map[int]chan struct{})
	kv.readSnapshot(persister.ReadSnapshot())
	kv.Debug("StartKVServer dedup=%v  kv=%v", kv.dedup, kv.kv)
	go kv.DoApply()

	return kv
}

func init() {
	v := os.Getenv("KV_VERBOSE")
	level := 0
	if v != "" {
		level, _ = strconv.Atoi(v)
	}
	if level < 1 {
		log.SetOutput(ioutil.Discard)
	}
}
