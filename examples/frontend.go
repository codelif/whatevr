// A whatevr frontend in fifty lines of Go, using nothing but the standard
// library: connect, say hello, subscribe to the chat list, print what arrives.
//
//	go run examples/frontend.go                  # watch the chat list
//	go run examples/frontend.go <chat_id> hi     # ...and send a message
//
// There is no client library to import and none is planned: the protocol is
// newline-delimited JSON on a unix socket (PROTOCOL.md), so this is the whole
// of it in any language with a socket and a JSON parser.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
)

func main() {
	socket := filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "whatevr", "whatevrd.sock")
	conn, err := net.Dial("unix", socket)
	if err != nil {
		log.Fatalf("dial %s: %v", socket, err)
	}
	defer conn.Close()

	enc := json.NewEncoder(conn)
	req := func(id int, method string, params map[string]any) {
		if err := enc.Encode(map[string]any{"id": id, "method": method, "params": params}); err != nil {
			log.Fatalf("write %s: %v", method, err)
		}
	}

	// The first request on a connection must be hello; everything else is
	// rejected until it succeeds.
	req(1, "hello", map[string]any{"client": "examples/frontend.go", "protocol": 1})
	req(2, "subscribe", map[string]any{"view": "chats", "filter": "all", "archived": false, "limit": 20})

	// Commands are fire-and-forget acks: the message you send comes back through
	// the views you subscribed to, never in the command's own response.
	if len(os.Args) > 2 {
		req(3, "send.text", map[string]any{"chat_id": os.Args[1], "text": os.Args[2]})
	}

	// Responses and events share the one stream, correlated by `id` and `sub`.
	// A real frontend would keep a map of items by item.id ordered by `sort`,
	// applying upserts and removes; this one just prints them.
	dec := json.NewDecoder(conn)
	for {
		var msg map[string]any
		if err := dec.Decode(&msg); err != nil {
			log.Fatalf("read: %v", err)
		}
		fmt.Printf("%v\n", msg)
	}
}
