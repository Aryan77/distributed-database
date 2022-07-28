# Fault tolerant database built on top of Raft

We now build a NOSQl Key-Value based database that uses our Raft implementation. The database service is a replicated state machine, consisting of several database servers that use Raft for replication. The database service will continue to process client requests as long as a majority of the servers are alive and can communicate, accounting for other failures.

Each client talks to the service through a `Clerk` with Put/Append/Get methods. A `Clerk` manages RPC interactions with the servers.

This service provides strong consistency to application calls to the `Clerk` Get/Put/Append methods: If called one at a time, the database methods act as if the system had only one copy of its state, and each call observes the modifications to the state implied by the preceding sequence of calls. For concurrent calls, the return values and final state is the same as if the operations had executed one at a time in some order.

The code for this implementation are in the `.go` files in `raft-db/`.

Each of the database servers will have an associated Raft peer. `Clerk`s send `Put()`, `Append()`, and `Get()` RPCs to the database server whose associated Raft is the leader. The database server code submits the Put/Append/Get operation to Raft, so that the Raft log holds a sequence of Put/Append/Get operations. All of the database servers execute operations from the Raft log in order, applying the operations to their key/value databases; the intent is for the servers to maintain identical replicas of the key/value database.

A `Clerk` sometimes doesn't know which database server is the Raft leader. If the `Clerk` sends an RPC to the wrong database server, or if it cannot reach the database server, the `Clerk` should re-try by sending to a different database server. If the database service commits the operation to its Raft log (and hence applies the operation to the database state machine), the leader reports the result to the `Clerk` by responding to its RPC. If the operation failed to commit (for example, if the leader was replaced), the server reports an error, and the `Clerk` retries with a different server.

The database servers do not directly communicate; they only interact with each other through Raft.

### Snapshots

Upto this point, a rebooting server replays the complete Raft log in order to restore its state. However, it's not practical for a long-running server to remember the complete Raft log forever. We introduce a modification that will allow Raft and the database server to cooperate to save space: from time to time the database server will persistently store a snapshot of its current state and Raft will discard the log entries that precede that snapshot. When a server restarts, it first installs a snapshot and then replays log entries from after the point at which the snapshot was created. 
