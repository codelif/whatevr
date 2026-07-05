#!/usr/bin/env bash
set -euo pipefail

socket="${WHATEVR_SOCKET:-${XDG_RUNTIME_DIR:?XDG_RUNTIME_DIR is not set}/whatevr/whatevrd.sock}"

{
    printf '%s\n' '{"id":1,"method":"hello","params":{"client":"examples/shell-frontend.sh","protocol":1}}'
    printf '%s\n' '{"id":2,"method":"subscribe","params":{"view":"chats","filter":"all","archived":false,"limit":20}}'
    while sleep 3600; do :; done
} | socat - "UNIX-CONNECT:${socket}" | jq -r '
    if .result.daemon then
        "connected: \(.result.daemon) protocol=\(.result.protocol) state=\(.result.state)"
    elif .result.sub then
        "subscribed: \(.result.sub)"
    elif .error then
        "error[\(.id)]: \(.error.code): \(.error.message)"
    elif .event == "upsert" then
        "chat sort=\(.sort) id=\(.item.id) name=\(.item.name // "") unread=\(.item.unread // 0) preview=\(.item.preview // "")"
    elif .event == "remove" then
        "remove chat id=\(.id)"
    elif .event == "ready" then
        "ready exhausted=\(.exhausted // false)"
    elif .event == "reset" then
        "reset"
    else
        tostring
    end
'
