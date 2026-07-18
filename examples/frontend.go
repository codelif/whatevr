//go:build ignore

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
)

func main() {
	conn, _ := net.Dial("unix", os.Getenv("XDG_RUNTIME_DIR")+"/whatevr/whatevrd.sock")
	defer conn.Close()

	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(conn)

	// read everything daemon sends: hello responses, command responses, events
	go func() {
		for {
			var msg map[string]any
			if err := dec.Decode(&msg); err != nil {
				log.Println("read:", err)
				return
			}
			fmt.Printf("daemon -> %#v\n", msg)
		}
	}()

	// write requests whenever you want
	enc.Encode(map[string]any{
		"id": 1, "method": "hello",
		"params": map[string]any{"client": "fastevr", "protocol": 1},
	})

	enc.Encode(map[string]any{
		"id": 2, "method": "subscribe",
		"params": map[string]any{"view": "chats", "limit": 50},
	})

	enc.Encode(map[string]any{
		"id": 3, "method": "send.text",
		"params": map[string]any{
			"chat_id": "917060029183@s.whatsapp.net",
			"text":    "hello",
		},
	})

	select {}
}
