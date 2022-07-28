# Raft

The first stage of building out this project entailed implementing Raft, a replicated state machine protocol.

A replicated service achieves fault tolerance by storing complete copies of its state (and thus its data) on more than one replica servers. This replication allows the service to continue working even if some of its servers fail (crash or are unable to connect to each other due to network issues). The issue with having this state distributed across multiple servers is that a failure may cause the replicas to hold different copies of the data.

Raft organizes client requests into a sequence, called the log, and ensures that all the replica servers see the same log. Each replica executes client requests in log order, applying them to its local copy of the service's state. Since all the live replicas see the same log contents, they all execute the same requests in the same order, and thus continue to have identical service state. If a server fails but later recovers, Raft takes care of bringing its log up to date. Raft will continue to operate as long as at least a majority of the servers are alive and can talk to each other. If there is no such majority, Raft will make no progress, but will pick up where it left off as soon as a majority can communicate again.

This first section implements Raft as a Go object type with associated methods, meant to be used as a module in a larger service. A set of Raft instances talk to each other with RPC to maintain replicated logs. The Raft interface will support an indefinite sequence of log entries, numbered with index numbers. When the log entry with a given index eventually gets committed, it should send the log entry to the larger service for it to execute.

The code for the implementation of Raft itself is in `raft/raft.go`. The primary interface that this supports:

```
// create a new Raft server instance:
rf := Make(peers, me, persister, applyCh)

// start agreement on a new log entry:
rf.Start(command interface{}) (index, term, isleader)

// ask a Raft for its current term, and whether it thinks it is leader
rf.GetState() (term, isLeader)

// each time a new entry is committed to the log, each Raft peer
// should send an ApplyMsg to the service (or tester).
type ApplyMsg
```

A service calls `Make(peers,me,…)` to create a Raft peer. The peers argument is an array of network identifiers of the Raft peers (including this one), for use with RPC. The `me` argument is the index of this peer in the peers array. `Start(command)` asks Raft to start the processing to append the command to the replicated log. `Start()` should return immediately, without waiting for the log appends to complete. The service expects your implementation to send an ApplyMsg for each newly committed log entry to the applyCh channel argument to `Make()`.

We use a custom RPC package that allows us to delay RPCs, re-order them, and discard them to simulate various network failures.

### Persistence

If a Raft-based server reboots it should resume service where it left off. This requires that Raft keep persistent state that survives a reboot.

Our implementation saves and restores persistent state from a `Persister` object (in `persister.go`). Calling `Raft.Make()` requires a `Persister` that initially holds the most recently persisted state if it exists.