#!/usr/bin/env bash
# A whole whatevr frontend in a shell script: it lists your chats, opens one
# conversation, and sends the lines you type — all over the NDJSON protocol on
# the daemon's unix socket, using nothing but socat and jq.
#
#   examples/shell-frontend.sh                     # just watch the chat list
#   examples/shell-frontend.sh <chat_id>           # also read + send to a chat
#
# Type a line and press enter to send it to <chat_id>; Ctrl-D stops typing but
# keeps watching; Ctrl-C quits.
#
# It renders every message through the item's `fallback` string, which is why it
# needs no cases for image, sticker or any kind added later (PROTOCOL.md rule 5:
# partial frontends are first-class citizens).
set -euo pipefail

socket="${WHATEVR_SOCKET:-${XDG_RUNTIME_DIR:?XDG_RUNTIME_DIR is not set}/whatevr/whatevrd.sock}"
chat="${1:-}"

# Outbound requests flow through a fifo so the startup handshake and the lines
# you type share one connection; socat reads the fifo and writes replies to jq.
req="$(mktemp -u)"; mkfifo "$req"; trap 'rm -f "$req"' EXIT

{
    printf '%s\n' '{"id":1,"method":"hello","params":{"client":"examples/shell-frontend.sh","protocol":1}}'
    printf '%s\n' '{"id":2,"method":"subscribe","params":{"view":"chats","filter":"all","archived":false,"limit":3}}'
    if [ -n "$chat" ]; then
        # Read the conversation (request id 3) and send each typed line to it.
        jq -cn --arg c "$chat" '{id:3,method:"subscribe",params:{view:"messages",chat_id:$c,anchor:"latest",limit:3}}'
        while IFS= read -r line; do
            jq -cn --arg c "$chat" --arg t "$line" '{id:9,method:"send.text",params:{chat_id:$c,text:$t}}'
        done
    fi
    # Never let the request stream hit EOF: an EOF here lets socat half-close and
    # race the daemon's in-flight snapshot (response → upserts → ready all ride
    # one connection). Idle until Ctrl-C so every frame has time to arrive.
    while sleep 3600; do :; done
} > "$req" &

# The daemon assigns each subscription its own `sub` id, unrelated to our request
# id (PROTOCOL.md rule 3). So learn sub → view from the subscribe responses
# (id 2 is chats, id 3 is messages) and route events by what we were told.
socat - "UNIX-CONNECT:${socket}" < "$req" | jq -rn --unbuffered '
    {"2":"chats","3":"messages"} as $roles
    | foreach inputs as $m ({role:{}};
        if $m.result.sub then .role[($m.result.sub|tostring)] = ($roles[$m.id|tostring] // "?") else . end
      ;
        (.role[$m.sub|tostring]) as $view
        | if   $m.result.daemon     then "connected: \($m.result.daemon) protocol=\($m.result.protocol) state=\($m.result.state)"
          elif $m.result.sub        then "subscribed: \($m.result.sub) (\($roles[$m.id|tostring] // "?"))"
          elif $m.result.message_id then "sent: \($m.result.message_id)"
          elif $m.error             then "error[\($m.id)]: \($m.error.code): \($m.error.message)"
          elif $m.event == "upsert" and $view == "chats"    then "chat  id=\($m.item.id) name=\($m.item.name // "") unread=\($m.item.unread // 0) preview=\($m.item.preview // "")"
          elif $m.event == "upsert" and $view == "messages" then "msg   \(if $m.item.direction == "outgoing" then "→" else "←" end) \($m.item.fallback)"
          elif $m.event == "remove" then "remove \($m.id) (sub \($m.sub))"
          elif $m.event == "ready"  then "ready sub=\($m.sub) exhausted=\($m.exhausted // false)"
          elif $m.event == "reset"  then "reset sub=\($m.sub)"
          else ($m|tostring) end)
'
