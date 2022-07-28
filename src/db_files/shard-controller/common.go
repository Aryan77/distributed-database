package shard_controller

const NShards = 10

type Config struct {
	Num    int
	Shards [NShards]int
	Groups map[int][]string
}

const (
	OK         = "OK"
	ErrTimeout = "ErrTimeout"
)

type Err string

type Args struct {
	ClientId  int32
	RequestId int64
}

type JoinArgs struct {
	Servers map[int][]string
	Args
}

type JoinReply struct {
	WrongLeader bool
	Err         Err
}

type LeaveArgs struct {
	GIDs []int
	Args
}

type LeaveReply struct {
	WrongLeader bool
	Err         Err
}

type MoveArgs struct {
	Shard int
	GID   int
	Args
}

type MoveReply struct {
	WrongLeader bool
	Err         Err
}

type QueryArgs struct {
	Num int
}

type QueryReply struct {
	WrongLeader bool
	Err         Err
	Config      Config
}
