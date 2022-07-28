package raft

import (
	"bytes"
	"dist-db/util_files/labgob"
	"dist-db/util_files/labrpc"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

type ApplyMsg struct {
	CommandValid bool
	Command      interface{}
	CommandIndex int

	SnapshotValid bool
	Snapshot      []byte
	SnapshotTerm  int
	SnapshotIndex int
}

type State int8

const (
	Follower State = iota
	Candidate
	Leader
)

func (s State) String() string {
	if s == Follower {
		return "F"
	} else if s == Candidate {
		return "C"
	} else if s == Leader {
		return "L"
	} else {
		panic("invalid state " + string(rune(s)))
	}
}

type LogEntry struct {
	Term    int
	Index   int
	Command interface{}
}

func (e *LogEntry) String() string {
	return fmt.Sprintf("{%d t%d %v}", e.Index, e.Term, e.Command)
}

type Raft struct {
	mu        sync.Mutex
	peers     []*labrpc.ClientEnd
	persister *Persister
	me        int
	dead      int32

	state           State
	term            int
	votedFor        int
	log             []*LogEntry
	lastHeartbeat   time.Time
	electionTimeout time.Duration

	commitIndex   int
	lastApplied   int
	applyCond     *sync.Cond
	nextIndex     []int
	matchIndex    []int
	replicateCond []*sync.Cond

	applyCh chan ApplyMsg
}

func (rf *Raft) IsMajority(vote int) bool {
	return vote >= rf.Majority()
}

func (rf *Raft) Majority() int {
	return len(rf.peers)/2 + 1
}

func (rf *Raft) GetLogAtIndex(logIndex int) *LogEntry {
	if logIndex < rf.log[0].Index {
		return nil
	}
	subscript := rf.LogIndexToSubscript(logIndex)
	if len(rf.log) > subscript {
		return rf.log[subscript]
	} else {
		return nil
	}
}

func (rf *Raft) LogIndexToSubscript(logIndex int) int {
	return logIndex - rf.log[0].Index
}

func (rf *Raft) LogTail() *LogEntry {
	return LogTail(rf.log)
}

func LogTail(xs []*LogEntry) *LogEntry {
	return xs[len(xs)-1]
}

func (rf *Raft) resetTerm(term int) {
	rf.term = term
	rf.votedFor = -1
	rf.persist()
}

func (rf *Raft) becomeFollower() {
	rf.state = Follower
}

func (rf *Raft) becomeCandidate() {
	rf.state = Candidate
}

func (rf *Raft) becomeLeader() {
	rf.state = Leader
}

func (rf *Raft) GetState() (int, bool) {

	var term int
	var isleader bool
	rf.mu.Lock()
	defer rf.mu.Unlock()
	term = rf.term
	isleader = rf.state == Leader
	return term, isleader
}

func (rf *Raft) persist() {
	rf.Debug(dPersist, "persist: %s", rf.FormatStateOnly())
	rf.persister.SaveRaftState(rf.serializeState())
}

func (rf *Raft) serializeState() []byte {
	w := new(bytes.Buffer)
	e := labgob.NewEncoder(w)
	_ = e.Encode(rf.term)
	_ = e.Encode(rf.votedFor)
	_ = e.Encode(rf.log)
	return w.Bytes()
}

func (rf *Raft) GetStateSize() int {
	return rf.persister.RaftStateSize()
}

func (rf *Raft) readPersist(data []byte) {
	if data == nil || len(data) < 1 { // bootstrap without any state?
		return
	}
	r := bytes.NewBuffer(data)
	d := labgob.NewDecoder(r)
	var term int
	var votedFor int
	var log []*LogEntry
	rf.mu.Lock()
	defer rf.mu.Unlock()
	if e := d.Decode(&term); e != nil {
		panic(e)
	}
	if e := d.Decode(&votedFor); e != nil {
		panic(e)
	}
	if e := d.Decode(&log); e != nil {
		panic(e)
	}

	rf.Debug(dPersist, "readPersist: t%d votedFor=S%d  current log: %v", term, votedFor, log)
	rf.term = term
	rf.votedFor = votedFor
	rf.log = log
	rf.commitIndex = rf.log[0].Index
	rf.lastApplied = rf.log[0].Index
}

type AppendEntriesArgs struct {
	Term         int
	LeaderId     int
	PrevLogIndex int
	PrevLogTerm  int
	Entries      []*LogEntry
	LeaderCommit int
}

type AppendEntriesReply struct {
	Term             int
	Success          bool
	ConflictingTerm  int
	ConflictingIndex int
}

func (rf *Raft) CondInstallSnapshot(lastIncludedTerm int, lastIncludedIndex int, snapshot []byte) bool {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	rf.Debug(dSnapshot, "CondInstallSnapshot lastIncludedTerm=%d lastIncludedIndex=%d  %s", lastIncludedTerm, lastIncludedIndex, rf.FormatStateOnly())

	if lastIncludedIndex < rf.commitIndex {
		rf.Debug(dSnapshot, "rejected outdated CondInstallSnapshot")
		return false
	}

	rf.Debug(dSnapshot, "before install snapshot: %s  full log: %v", rf.FormatStateOnly(), rf.FormatFullLog())
	if lastIncludedIndex >= rf.LogTail().Index {
		rf.log = rf.log[0:1]
	} else {
		rf.log = rf.log[rf.LogIndexToSubscript(lastIncludedIndex):]
	}
	rf.log[0].Index = lastIncludedIndex
	rf.log[0].Term = lastIncludedTerm
	rf.log[0].Command = nil
	rf.commitIndex = lastIncludedIndex
	rf.lastApplied = lastIncludedIndex
	rf.Debug(dSnapshot, "after install snapshot: %s  full log: %v", rf.FormatStateOnly(), rf.FormatFullLog())
	rf.persister.SaveStateAndSnapshot(rf.serializeState(), snapshot)
	return true
}

func (rf *Raft) Snapshot(index int, snapshot []byte) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	rf.Debug(dSnapshot, "snapshot until %d", index)
	for _, entry := range rf.log {
		if entry.Index == index {
			rf.Debug(dSnapshot, "before snapshot:  full log: %v", rf.FormatFullLog())
			rf.log = rf.log[rf.LogIndexToSubscript(entry.Index):]
			rf.log[0].Command = nil
			rf.Debug(dSnapshot, "after snapshot:  full log: %s", rf.FormatFullLog())
			rf.persister.SaveStateAndSnapshot(rf.serializeState(), snapshot)
			return
		}
	}
}

func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	now := time.Now()
	rf.mu.Lock()
	defer rf.mu.Unlock()
	reply.Term = rf.term
	if args.Term < rf.term {
		rf.Debug(dLog, "received term %d < currentTerm %d from S%d, reject AppendEntries", args.Term, rf.term, args.LeaderId)
		reply.Success = false
		return
	}
	rf.lastHeartbeat = now

	rf.Debug(dLog, "receive AppendEntries from S%d args.term=%d %+v", args.LeaderId, args.Term, args)
	if rf.state == Candidate && args.Term >= rf.term {
		rf.Debug(dLog, "received term %d >= currentTerm %d from S%d, leader is legitimate", args.Term, rf.term, args.LeaderId)
		rf.becomeFollower()
		rf.resetTerm(args.Term)
	} else if rf.state == Follower && args.Term > rf.term {
		rf.Debug(dLog, "received term %d > currentTerm %d from S%d, reset rf.term", args.Term, rf.term, args.LeaderId)
		rf.resetTerm(args.Term)
	} else if rf.state == Leader && args.Term > rf.term {
		rf.Debug(dLog, "received term %d > currentTerm %d from S%d, back to Follower", args.Term, rf.term, args.LeaderId)
		rf.becomeFollower()
		rf.resetTerm(args.Term)
	}

	if args.PrevLogIndex > 0 && args.PrevLogIndex >= rf.log[0].Index {
		if prev := rf.GetLogAtIndex(args.PrevLogIndex); prev == nil || prev.Term != args.PrevLogTerm {
			rf.Debug(dLog, "log consistency check failed. local log at prev {%d t%d}: %+v  full log: %s", args.PrevLogIndex, args.PrevLogTerm, prev, rf.FormatFullLog())
			if prev != nil {
				for _, entry := range rf.log {
					if entry.Term == prev.Term {
						reply.ConflictingIndex = entry.Index
						break
					}
				}
				reply.ConflictingTerm = prev.Term
			} else {
				reply.ConflictingIndex = rf.LogTail().Index
				reply.ConflictingTerm = 0
			}
			reply.Success = false

			return
		}
	}
	if len(args.Entries) > 0 {
		rf.Debug(dLog, "before merge: %s", rf.FormatLog())
		if LogTail(args.Entries).Index >= rf.log[0].Index {
			appendLeft := 0
			for i, entry := range args.Entries {
				if local := rf.GetLogAtIndex(entry.Index); local != nil {
					if local.Index != entry.Index {
						panic(rf.Sdebug(dFatal, "LMP violated: local.Index != entry.Index. entry: %+v  local log at entry.Index: %+v", entry, local))
					}
					if local.Term != entry.Term {
						rf.Debug(dLog, "merge conflict at %d", i)
						rf.log = rf.log[:rf.LogIndexToSubscript(entry.Index)]
						appendLeft = i
						break
					}
					appendLeft = i + 1
				}
			}
			for i := appendLeft; i < len(args.Entries); i++ {
				entry := *args.Entries[i]
				rf.log = append(rf.log, &entry)
			}
		}
		rf.Debug(dLog, "after merge: %s", rf.FormatLog())
		rf.persist()
	}
	if args.LeaderCommit > rf.commitIndex {
		rf.commitIndex = Min(args.LeaderCommit, rf.LogTail().Index)
		rf.applyCond.Broadcast()
	}
	rf.Debug(dLog, "finish process heartbeat: commitIndex=%d", rf.commitIndex)
	reply.Success = true
}

func (rf *Raft) sendAppendEntries(server int, args *AppendEntriesArgs, reply *AppendEntriesReply) bool {
	return rf.peers[server].Call("Raft.AppendEntries", args, reply)
}

func Min(a int, b int) int {
	if a > b {
		return b
	} else {
		return a
	}
}

func Max(a int, b int) int {
	if a > b {
		return a
	} else {
		return b
	}
}

type RequestVoteArgs struct {
	Term         int
	CandidateId  int
	LastLogIndex int
	LastLogTerm  int
}

type RequestVoteReply struct {
	Term        int
	VoteGranted bool
}

func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	now := time.Now()
	rf.mu.Lock()
	defer rf.mu.Unlock()
	reply.Term = rf.term
	if rf.votedFor == -1 {
		rf.Debug(dVote, "S%d RequestVote %+v votedFor=<nil>", args.CandidateId, args)
	} else {
		rf.Debug(dVote, "S%d RequestVote %+v votedFor=S%d", args.CandidateId, args, rf.votedFor)
	}
	if args.Term < rf.term {
		reply.VoteGranted = false
		return
	}

	if args.Term > rf.term {
		rf.Debug(dVote, "received term %d > currentTerm %d, back to Follower", args.Term, rf.term)
		rf.becomeFollower()
		rf.resetTerm(args.Term)
	}
	if (rf.votedFor == -1 || rf.votedFor == args.CandidateId) && rf.isAtLeastUpToDate(args) {
		rf.votedFor = args.CandidateId
		rf.persist()
		rf.lastHeartbeat = now
		reply.VoteGranted = true
	} else {
		reply.VoteGranted = false
	}
}

func (rf *Raft) isAtLeastUpToDate(args *RequestVoteArgs) bool {
	b := false
	if args.LastLogTerm == rf.LogTail().Term {
		b = args.LastLogIndex >= rf.LogTail().Index
	} else {
		b = args.LastLogTerm >= rf.LogTail().Term
	}
	if !b {
		rf.Debug(dVote, "hands down RequestVote from S%d  %+v  full log: %v", args.CandidateId, args, rf.FormatFullLog())
	}
	return b
}

func (rf *Raft) sendRequestVote(server int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
	ok := rf.peers[server].Call("Raft.RequestVote", args, reply)
	return ok
}

type InstallSnapshotArgs struct {
	Term              int
	LeaderId          int
	LastIncludedIndex int
	LastIncludedTerm  int
	Data              []byte
}

type InstallSnapshotReply struct {
	Term int
}

func (rf *Raft) InstallSnapshot(args *InstallSnapshotArgs, reply *InstallSnapshotReply) {
	now := time.Now()
	rf.mu.Lock()
	reply.Term = rf.term
	if args.Term < rf.term {
		rf.Debug(dSnapshot, "received term %d < currentTerm %d from S%d, reject InstallSnapshot")
		rf.mu.Unlock()
		return
	}
	rf.lastHeartbeat = now

	rf.Debug(dSnapshot, "receive InstallSnapshot from S%d args.term=%d LastIncludedIndex=%d LastIncludedTerm=%d", args.LeaderId, args.Term, args.LastIncludedIndex, args.LastIncludedTerm)
	if rf.state == Candidate && args.Term >= rf.term {
		rf.Debug(dSnapshot, "received term %d >= currentTerm %d from S%d, leader is legitimate", args.Term, rf.term, args.LeaderId)
		rf.becomeFollower()
		rf.resetTerm(args.Term)
	} else if rf.state == Follower && args.Term > rf.term {
		rf.Debug(dSnapshot, "received term %d > currentTerm %d from S%d, reset rf.term", args.Term, rf.term, args.LeaderId)
		rf.resetTerm(args.Term)
	} else if rf.state == Leader && args.Term > rf.term {
		rf.Debug(dSnapshot, "received term %d > currentTerm %d from S%d, back to Follower", args.Term, rf.term, args.LeaderId)
		rf.becomeFollower()
		rf.resetTerm(args.Term)
	}
	rf.mu.Unlock()

	go func() {
		rf.applyCh <- ApplyMsg{
			CommandValid:  false,
			SnapshotValid: true,
			Snapshot:      args.Data,
			SnapshotTerm:  args.LastIncludedTerm,
			SnapshotIndex: args.LastIncludedIndex,
		}
	}()
}

func (rf *Raft) sendInstallSnapshot(server int, args *InstallSnapshotArgs, reply *InstallSnapshotReply) bool {
	return rf.peers[server].Call("Raft.InstallSnapshot", args, reply)
}

const (
	ElectionTimeoutMax = int64(600 * time.Millisecond)
	ElectionTimeoutMin = int64(500 * time.Millisecond)
	HeartbeatInterval  = 100 * time.Millisecond
)

func NextElectionTimeout() time.Duration {
	rand.Seed(time.Now().UnixNano())
	return time.Duration(rand.Int63n(ElectionTimeoutMax-ElectionTimeoutMin) + ElectionTimeoutMin) // already Millisecond
}

func (rf *Raft) DoElection() {
	for !rf.killed() {
		time.Sleep(rf.electionTimeout)
		rf.mu.Lock()
		if rf.state == Leader {
			rf.mu.Unlock()
			continue
		}
		if time.Since(rf.lastHeartbeat) >= rf.electionTimeout {
			rf.electionTimeout = NextElectionTimeout()
			rf.resetTerm(rf.term + 1)
			rf.becomeCandidate()
			rf.votedFor = rf.me
			rf.persist()
			rf.Debug(dElection, "electionTimeout %dms elapsed, turning to Candidate", rf.electionTimeout/time.Millisecond)

			args := &RequestVoteArgs{
				Term:         rf.term,
				CandidateId:  rf.me,
				LastLogIndex: rf.LogTail().Index,
				LastLogTerm:  rf.LogTail().Term,
			}
			rf.mu.Unlock()

			vote := uint32(1)
			for i := range rf.peers {
				if i == rf.me {
					continue
				}
				go func(i int, args *RequestVoteArgs) {
					var reply RequestVoteReply
					ok := rf.sendRequestVote(i, args, &reply)
					if !ok {
						return
					}
					rf.mu.Lock()
					defer rf.mu.Unlock()
					rf.Debug(dElection, "S%d RequestVoteReply %+v rf.term=%d args.Term=%d", i, reply, rf.term, args.Term)
					if rf.state != Candidate || rf.term != args.Term {
						return
					}
					if reply.Term > rf.term {
						rf.Debug(dElection, "return to Follower due to reply.Term > rf.term")
						rf.becomeFollower()
						rf.resetTerm(reply.Term)
						return
					}
					if reply.VoteGranted {
						rf.Debug(dElection, "<- S%d vote received", i)
						vote += 1

						if rf.IsMajority(int(vote)) {
							for i = 0; i < len(rf.peers); i++ {
								rf.nextIndex[i] = rf.LogTail().Index + 1
								rf.matchIndex[i] = 0
							}
							rf.matchIndex[rf.me] = rf.LogTail().Index
							rf.Debug(dLeader, "majority vote (%d/%d) received, turning Leader  %s", vote, len(rf.peers), rf.FormatState())
							rf.becomeLeader()
							rf.BroadcastHeartbeat()
						}
					}
				}(i, args)
			}
			rf.mu.Lock()
		}
		rf.mu.Unlock()
	}
}

func (rf *Raft) BroadcastHeartbeat() {
	rf.Debug(dHeartbeat, "BroadcastHeartbeat start")
	rf.mu.Unlock()

	for i := range rf.peers {
		if i == rf.me {
			continue
		}

		go rf.Replicate(i)
	}

	rf.mu.Lock()
}

func (rf *Raft) DoHeartbeat() {
	for !rf.killed() {
		time.Sleep(HeartbeatInterval)
		if !rf.needHeartbeat() {
			continue
		}

		rf.mu.Lock()
		rf.BroadcastHeartbeat()
		rf.mu.Unlock()
	}
}

func (rf *Raft) needHeartbeat() bool {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.state == Leader
}

func (rf *Raft) DoApply(applyCh chan ApplyMsg) {
	rf.applyCond.L.Lock()
	defer rf.applyCond.L.Unlock()
	for {
		for !rf.needApply() {
			rf.applyCond.Wait()
			if rf.killed() {
				return
			}
		}

		rf.mu.Lock()
		if !rf.needApplyL() {
			rf.mu.Unlock()
			continue
		}
		rf.lastApplied += 1
		entry := rf.GetLogAtIndex(rf.lastApplied)
		if entry == nil {
			panic(rf.Sdebug(dFatal, "entry == nil  %s", rf.FormatState()))
		}
		toCommit := *entry
		rf.Debug(dCommit, "apply rf[%d]=%+v", rf.lastApplied, toCommit)
		rf.mu.Unlock()

		applyCh <- ApplyMsg{
			Command:      toCommit.Command,
			CommandValid: true,
			CommandIndex: toCommit.Index,
		}
	}
}

func (rf *Raft) needApply() bool {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	rf.Debug(dTrace, "needApply: commitIndex=%d lastApplied=%d", rf.commitIndex, rf.lastApplied)
	return rf.needApplyL()
}

func (rf *Raft) needApplyL() bool {
	return rf.commitIndex > rf.lastApplied
}

func (rf *Raft) DoReplicate(peer int) {
	rf.replicateCond[peer].L.Lock()
	defer rf.replicateCond[peer].L.Unlock()
	for {
		for !rf.needReplicate(peer) {
			rf.replicateCond[peer].Wait()
			if rf.killed() {
				return
			}
		}

		rf.Replicate(peer)
	}
}

func (rf *Raft) needReplicate(peer int) bool {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	nextIndex := rf.nextIndex[peer]
	rf.Debug(dTrace, "needReplicate: nextIndex=%v  log tail %+v", rf.nextIndex, rf.LogTail())
	return rf.state == Leader && peer != rf.me && rf.GetLogAtIndex(nextIndex) != nil && rf.LogTail().Index > nextIndex
}

func (rf *Raft) Replicate(peer int) {
	rf.mu.Lock()
	if rf.state != Leader {
		rf.mu.Unlock()
		return
	}
	if rf.nextIndex[peer] <= rf.log[0].Index {
		var installReply InstallSnapshotReply
		installArgs := &InstallSnapshotArgs{
			Term:              rf.term,
			LeaderId:          rf.me,
			LastIncludedIndex: rf.log[0].Index,
			LastIncludedTerm:  rf.log[0].Term,
			Data:              rf.persister.ReadSnapshot(),
		}
		rf.mu.Unlock()

		installOk := rf.sendInstallSnapshot(peer, installArgs, &installReply)
		if !installOk {
			return
		}

		rf.mu.Lock()
		rf.Debug(dSnapshot, "S%d InstallSnapshotReply %+v rf.term=%d installArgs.Term=%d", peer, installReply, rf.term, installArgs.Term)
		if rf.term != installArgs.Term {
			rf.mu.Unlock()
			return
		}
		if installReply.Term > rf.term {
			rf.Debug(dSnapshot, "return to Follower due to installReply.Term > rf.term")
			rf.becomeFollower()
			rf.resetTerm(installReply.Term)
			rf.mu.Unlock()
			return
		}
		rf.nextIndex[peer] = installArgs.LastIncludedIndex + 1
		rf.mu.Unlock()
		return
	}

	var entries []*LogEntry
	nextIndex := rf.nextIndex[peer]
	for j := nextIndex; j <= rf.LogTail().Index; j++ {
		atIndex := rf.GetLogAtIndex(j)
		if atIndex == nil {
			panic(rf.Sdebug(dFatal, "atIndex == nil  %s", rf.FormatState()))
		}
		entry := *atIndex
		entries = append(entries, &entry)
	}
	prev := rf.GetLogAtIndex(nextIndex - 1)
	rf.Debug(dWarn, "replicate S%d nextIndex=%v matchIndex=%v prevLog: %v", peer, rf.nextIndex, rf.matchIndex, prev)
	args := &AppendEntriesArgs{
		Term:         rf.term,
		LeaderId:     rf.me,
		PrevLogIndex: prev.Index,
		PrevLogTerm:  prev.Term,
		LeaderCommit: rf.commitIndex,
		Entries:      entries,
	}
	rf.mu.Unlock()

	rf.Sync(peer, args)
}

func (rf *Raft) Sync(peer int, args *AppendEntriesArgs) {
	var reply AppendEntriesReply
	ok := rf.sendAppendEntries(peer, args, &reply)
	if !ok {
		return
	}

	rf.mu.Lock()
	defer rf.mu.Unlock()
	rf.Debug(dHeartbeat, "S%d AppendEntriesReply %+v rf.term=%d args.Term=%d", peer, reply, rf.term, args.Term)
	if rf.term != args.Term {
		rf.Debug(dHeartbeat, "S%d AppendEntriesReply rejected since rf.term(%d) != args.Term(%d)", peer, rf.term, args.Term)
		return
	}
	if reply.Term > rf.term {
		rf.Debug(dHeartbeat, "return to Follower due to reply.Term > rf.term")
		rf.becomeFollower()
		rf.resetTerm(reply.Term)
	} else {
		if reply.Success {
			if len(args.Entries) == 0 {
				return
			}
			logTailIndex := LogTail(args.Entries).Index
			rf.matchIndex[peer] = logTailIndex
			rf.nextIndex[peer] = logTailIndex + 1
			rf.Debug(dHeartbeat, "S%d logTailIndex=%d commitIndex=%d lastApplied=%d matchIndex=%v nextIndex=%v", peer, logTailIndex, rf.commitIndex, rf.lastApplied, rf.matchIndex, rf.nextIndex)

			preCommitIndex := rf.commitIndex
			for i := rf.commitIndex; i <= logTailIndex; i++ {
				count := 0
				for p := range rf.peers {
					if rf.matchIndex[p] >= i {
						count += 1
					}
				}
				if rf.IsMajority(count) && rf.GetLogAtIndex(i).Term == rf.term {
					preCommitIndex = i
				}
			}
			rf.commitIndex = preCommitIndex

			if rf.commitIndex > rf.lastApplied {
				rf.applyCond.Broadcast()
			}
		} else {
			if reply.Term < rf.term {
				rf.Debug(dHeartbeat, "S%d false negative reply rejected")
				return
			}
			nextIndex := rf.nextIndex[peer]
			rf.matchIndex[peer] = 0

			if reply.ConflictingTerm > 0 {
				for i := len(rf.log) - 1; i >= 1; i-- {
					if rf.log[i].Term == reply.ConflictingTerm {
						rf.nextIndex[peer] = Min(nextIndex, rf.log[i].Index+1)
						rf.Debug(dHeartbeat, "S%d old_nextIndex: %d new_nextIndex: %d  full log: %s", peer, nextIndex, rf.nextIndex[peer], rf.FormatFullLog())
						return
					}
				}
			}
			rf.nextIndex[peer] = Max(Min(nextIndex, reply.ConflictingIndex), 1)
			rf.Debug(dHeartbeat, "S%d old_nextIndex: %d new_nextIndex: %d  full log: %s", peer, nextIndex, rf.nextIndex[peer], rf.FormatFullLog())
		}
	}
}

func (rf *Raft) Start(command interface{}) (int, int, bool) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	if rf.state != Leader {
		return -1, -1, false
	}
	entry := LogEntry{
		Term:    rf.term,
		Index:   rf.LogTail().Index + 1,
		Command: command,
	}
	rf.log = append(rf.log, &entry)
	rf.persist()
	rf.matchIndex[rf.me] += 1
	rf.Debug(dClient, "client start replication with entry %v  %s", entry, rf.FormatState())
	rf.BroadcastHeartbeat()
	return entry.Index, rf.term, rf.state == Leader
}

func (rf *Raft) Kill() {
	atomic.StoreInt32(&rf.dead, 1)
}

func (rf *Raft) killed() bool {
	z := atomic.LoadInt32(&rf.dead)
	return z == 1
}

func Make(peers []*labrpc.ClientEnd, me int,
	persister *Persister, applyCh chan ApplyMsg) *Raft {
	rf := &Raft{}
	rf.peers = peers
	rf.persister = persister
	rf.me = me
	rf.votedFor = -1
	rf.applyCh = applyCh
	rf.applyCond = sync.NewCond(&sync.Mutex{})

	rf.electionTimeout = NextElectionTimeout()
	rf.log = append(rf.log, &LogEntry{
		Term:    0,
		Index:   0,
		Command: nil,
	})

	go rf.DoElection()
	go rf.DoHeartbeat()
	go rf.DoApply(applyCh)
	for range rf.peers {
		rf.replicateCond = append(rf.replicateCond, sync.NewCond(&sync.Mutex{}))
		rf.nextIndex = append(rf.nextIndex, 1)
		rf.matchIndex = append(rf.matchIndex, 0)
	}
	for i := range rf.peers {
		if i == rf.me {
			continue
		}
		go rf.DoReplicate(i)
	}
	if len(rf.log) != 1 {
		panic("len(rf.log) != 1")
	}

	rf.readPersist(persister.ReadRaftState())

	return rf
}
