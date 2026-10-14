package state

import "sync/atomic"

type atomicSnap = atomic.Pointer[Snapshot]
