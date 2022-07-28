# Sharded Database

With the shard-controller implemented, we can finally implement our sharded database.

Each sharded database server operates as part of a replica group. Each replica group serves Get, Put, and Append operations for some of the key-space shards. Multiple replica groups cooperate to serve the complete set of shards. A single instance of the shard-controller service assigns shards to replica groups; when this assignment changes, replica groups have to hand off shards to each other, while ensuring that clients do not see inconsistent responses.

The database provides a linearizable interface to applications that use its client interface. That is, completed application calls to the `Clerk.Get()`, `Clerk.Put()`, and `Clerk.Append()` methods in must appear to have affected all replicas in the same order. This must be true even when Gets and Puts arrive at about the same time as configuration changes.

We also need to handle the problem of configuration changes. The servers watch for configuration changes, and when one is detected, starts the shard migration process. If a replica group loses a shard, it stops serving requests to keys in that shard and starts migrating the data for that shard to the replica group that takes over ownership. 