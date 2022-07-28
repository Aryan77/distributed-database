# Distributed, persistent, sharded, fault-tolerant NOSQL Database

This project aims to build an advanced NOSQL database from scratch in Go, inspired by the likes of Google's BigTable and Amazon's DynamoDB - which need to be:
1. Distributed across multiple servers - both because they store so much data and so that if one fails the others can handle the load (thus persistent and fault-tolerant), and
2. Extremely performant - these databases are storing a massive amount of data and are processing upwards of millions of requests every second - it is important that they are very fast.
3. Reliable and correct - Allowing so many concurrent connections is bound to cause a lot of difficulties in making sure the data in the database at any given point is correct; the difficulty of this is compounded by the fact that the data is distributed across multiple servers, all of which have the potential to fail and mis-communicate.

I decided to address (1) and (3) using a consensus protocol called Raft which you can read about here (I chose to use it over Paxos as my initial Paxos implementation was extremely bug-ridden and was taking an exorbitant amount of time to debug), and (2) using a technique called sharding which partitions the database by its keys and distributes it across replica groups so the load on any one replica group is reduced.

In order to make sure that my implementation was correct - I made use of MIT's excellent test suite for their distributed systems course - 6.824. Distributed systems like this are notoriously difficult to test, and I would have most definitely missed many edge cases if I set out to write the entire test suite by myself. My implementation passes all of their tests - I will describe below how you can verify that for yourself.

## Source files description: 

Inside the `src/` directory, you will find the following directories:
1. `test_files/` - I do not use these myself, but MIT's test suite makes use of them for testing. 
2. `util_files/` - These are MIT's version of RPC and encoding libraries, with some additions that allow us to deliberately simulate a network that can lose requests, lose responses, delay messages, and entirely disconnect particular hosts. 
3. `db_files/` - These contain the files for the actual database implementation. There are 4 directories - which implement the database in a step-by-step manner and which can be tested individually:

    i. `raft/` - This contains the implementation of the Raft protocol.

    ii. `raft-db/` - This builds a basic fault-tolerant database using the Raft protocol - so the database is already distributed and persistent at this point. 

    iii. `shard-controller/` - We now add sharding for performance, and as a first step we create a Shard Controller that handles the configuration and oversees the handling of the different replica groups and partitions. 

    iv. `raft-db-sharded/` - Finally, we add sharding to the database itself and obtain very high performance. 

    You can run the test suite for any of these 4 directories by navigating inside them and running `go test`. The `README.md` files inside each of the 4 directories further describe the interface of each of the implementations and what exactly they are doing. 

## Roadmap for this project:

I intend to build a proper, usable interface to the whole database so someone who does not understand the code itself can use it - I want to hide the complexity of handling all the problems that come with creating a distributed system so the end user can simply use this as a regular NOSQL database. Currently, this project is only appropriate for a programmer that can understand the source code well and is able to figure out how to use the test cases to build out their own implementation. I will also document the project better, since I plan on extending it a fair amount.
