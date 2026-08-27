package converter

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"sync/atomic"
	"time"
)

var (
	timeNow          = time.Now
	nextSubscriberID atomic.Int64
	taskSequence     atomic.Uint64
)

func newTaskID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err == nil {
		return hex.EncodeToString(buf[:])
	}
	return strconvFormatUint(taskSequence.Add(1))
}

func strconvFormatUint(value uint64) string {
	return strconv.FormatUint(value, 36)
}
