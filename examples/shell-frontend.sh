#!/usr/bin/env bash
# A whole whatevr frontend in a shell script: it lists your chats, opens one
# conversation, and sends the lines you type — all over the NDJSON protocol on
# the daemon's unix socket, using nothing but socat and jq.
#
#   examples/shell-frontend.sh                     # just watch the chat list
#   examples/shell-frontend.sh <chat_id>           # also read + send to a chat
#
# Type a line and press enter to send it to <chat_id>; Ctrl-D or Ctrl-C to quit.
set -euo pipefail

socket="${WHATEVR_SOCKET:-${XDG_RUNTIME_DIR:?XDG_RUNTIME_DIR is not set}/whatevr/whatevrd.sock}"
chat="${1:-}"

# Outbound requests flow through a fifo so the startup handshake and the lines
# you type share one connection; socat reads the fifo and writes replies to jq.
req="$(mktemp -u)"; mkfifo "$req"; trap 'rm -f "$req"' EXIT

{
    printf '%s\n' '{"id":1,"method":"hello","params":{"client":"examples/shell-frontend.sh","protocol":1}}'
    printf '%s\n' '{"id":2,"method":"subscribe","params":{"view":"chats","filter":"all","archived":false,"limit":20}}'
    if [ -n "$chat" ]; then
        # Read the conversation (sub 3) and send each typed line to it.
        jq -cn --arg c "$chat" '{id:3,method:"subscribe",params:{view:"messages",chat_id:$c,anchor:"latest",limit:20}}'
        while IFS= read -r line; do
            jq -cn --arg c "$chat" --arg t "$line" '{id:9,method:"send.text",params:{chat_id:$c,text:$t}}'
        done
    else
        while sleep 3600; do :; done
    fi
} > "$req" &

socat - "UNIX-CONNECT:${socket}" < "$req" | jq -r '
    if .result.daemon then "connected: \(.result.daemon) protocol=\(.result.protocol) state=\(.result.state)"
    elif .result.sub then "subscribed: \(.result.sub)"
    elif .result.message_id then "sent: \(.result.message_id)"
    elif .error then "error[\(.id)]: \(.error.code): \(.error.message)"
    elif .event == "upsert" and .sub == 2 then "chat  id=\(.item.id) name=\(.item.name // "") unread=\(.item.unread // 0) preview=\(.item.preview // "")"
    elif .event == "upsert" and .sub == 3 then "msg   \(if .item.direction == "outgoing" then "→" else "←" end) \(.item.text // "[\(.item.kind)]")"
    elif .event == "remove" then "remove \(.id) (sub \(.sub))"
    elif .event == "ready" then "ready sub=\(.sub) exhausted=\(.exhausted // false)"
    elif .event == "reset" then "reset sub=\(.sub)"
    else tostring end
'
