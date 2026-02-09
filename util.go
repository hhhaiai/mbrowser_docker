package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"time"
)

func newOAID() string {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func newUserKey() string {
	buf := make([]byte, 12)
	_, _ = rand.Read(buf)
	return "anon_" + hex.EncodeToString(buf)
}

func newMiID() string {
	// 10-digit numeric string
	n, _ := rand.Int(rand.Reader, big.NewInt(9000000000))
	return n.Add(n, big.NewInt(1000000000)).String()
}

func newConversationID(oaid string) string {
	return oaid + fmt.Sprintf("%d", nowMillis())
}

func newSearchID(oaid string) string {
	return oaid + fmt.Sprintf("%d", nowMillis())
}

func nowMillis() int64 {
	return time.Now().UnixNano() / int64(time.Millisecond)
}
