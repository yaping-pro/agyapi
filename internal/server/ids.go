package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// generateChatID returns an OpenAI-shaped completion id. It only has to be
// unique per response; clients treat it as opaque.
func generateChatID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	}
	return "chatcmpl-" + hex.EncodeToString(b)
}
