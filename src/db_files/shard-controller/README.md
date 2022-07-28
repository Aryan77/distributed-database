# Sharded distributed database

Finally, we now add sharding to our database service - the keys are sharded, or partitioned, over a set of replica groups in order to make our database extremely performant: each replica group handles operations for just a few of the shards, and the groups operate in parallel; thus total system throughput increases in proportion to the number of groups.

The sharded database will have two main components. First, a set of replica groups. Each replica group is responsible for a subset of the shards. A replica consists of a handful of servers that use Raft to replicate the group's shards. The second component is the "shard controller". The shard controller decides which replica group should serve each shard; this information is called the configuration. The configuration changes over time. Clients consult the shard controller in order to find the replica group for a key, and replica groups consult the master in order to find out what shards to serve. There is a single shard controller for the whole system, implemented as a fault-tolerant service using Raft. A key feature of this sharded database is that it must be able to shift shards among replica groups to prevent any single group being overloaded.

There is a fair bit of nuance to handling reconfiguration in this system: within a single replica group, all group members must agree on when a reconfiguration occurs relative to client requests. For example, a Put may arrive at about the same time as a reconfiguration that causes the replica group to stop being responsible for the shard holding the Put's key. All replicas in the group must agree on whether the Put occurred before or after the reconfiguration. If before, the Put should take effect and the new owner of the shard will see its effect; if after, the Put won't take effect and client must re-try at the new owner. The way this is implemented is extending Raft to not only log the sequence of operations, but also the sequence of reconfigurations, while ensuring that at most one replica group is serving requests for each shard at any one time.

### Shard Controller

The implementation of the Shard Controller is in the `shard-controller/` directory.

The shard-controller manages a sequence of numbered configurations. Each configuration describes a set of replica groups and an assignment of shards to replica groups. Whenever this assignment needs to change, the shard-controller creates a new configuration with the new assignment. Key/value clients and servers contact the shard-controller when they want to know the current (or a past) configuration.

The interface for the shard-controller is implemented in `shard-controller/common.go`, which consists of Join, Leave, Move, and Query RPCs. These RPCs are intended to allow an administrator to control the Shard Controller: to add new replica groups, to eliminate replica groups, and to move shards between replica groups.

The Join RPC is used by an administrator to add new replica groups. Its argument is a set of mappings from unique, non-zero replica group identifiers (GIDs) to lists of server names. The shard controller reacts by creating a new configuration that includes the new replica groups, which divides the shards as evenly as possible among the full set of groups.

The Leave RPC's argument is a list of GIDs of previously joined groups. The shard controller creates a new configuration that does not include those groups, and that assigns those groups' shards to the remaining groups. The new configuration should divide the shards as evenly as possible among the groups.

The Move RPC's arguments are a shard number and a GID. The shard controller creates a new configuration in which the shard is assigned to the group.

The Query RPC's argument is a configuration number. The shardmaster replies with the configuration that has that number. If the number is -1 or bigger than the biggest known configuration number, the shardmaster should reply with the latest configuration. The result of Query(-1) should reflect every Join, Leave, or Move RPC that the shardmaster finished handling before it received the Query(-1) RPC.