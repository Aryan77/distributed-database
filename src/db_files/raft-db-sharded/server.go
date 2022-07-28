package raft_db_sharded

import (
	raft2 "dist-db/db_files/raft"
	"dist-db/util_files/labgob"
	"dist-db/util_files/labrpc"
)
import "sync"

type Op struct {
}

type ShardKV struct {
	mu           sync.Mutex
	me           int
	rf           *raft2.Raft
	applyCh      chan raft2.ApplyMsg
	make_end     func(string) *labrpc.ClientEnd
	gid          int
	ctrlers      []*labrpc.ClientEnd
	maxraftstate int
}

func (kv *ShardKV) Get(args *GetArgs, reply *GetReply) {
}

func (kv *ShardKV) PutAppend(args *PutAppendArgs, reply *PutAppendReply) {
}

func (kv *ShardKV) Kill() {
	kv.rf.Kill()
}

func StartServer(servers []*labrpc.ClientEnd, me int, persister *raft2.Persister, maxraftstate int, gid int, ctrlers []*labrpc.ClientEnd, make_end func(string) *labrpc.ClientEnd) *ShardKV {
	labgob.Register(Op{})

	kv := new(ShardKV)
	kv.me = me
	kv.maxraftstate = maxraftstate
	kv.make_end = make_end
	kv.gid = gid
	kv.ctrlers = ctrlers

	kv.applyCh = make(chan raft2.ApplyMsg)
	kv.rf = raft2.Make(servers, me, persister, kv.applyCh)

	return kv
}
