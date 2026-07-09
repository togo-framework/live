#!/usr/bin/env bash
# Part-1 HTTP-level end-to-end proof of the live echo loop.
# Starts the example app, exercises the REST API, asserts the agent reply.
set -euo pipefail
cd "$(dirname "$0")"

PORT=8099
BASE="http://localhost:${PORT}"
rm -f live-example.db

echo "== starting live-example server =="
GOFLAGS=-mod=mod go run . >/tmp/live-example.log 2>&1 &
SRV=$!
trap 'kill $SRV 2>/dev/null || true' EXIT

# wait for the server to answer
for i in $(seq 1 60); do
  if curl -fs "${BASE}/api/live/agents" >/dev/null 2>&1; then break; fi
  sleep 0.5
done

echo
echo "== GET /api/live/agents (seeded 'Ada' agent) =="
curl -fs "${BASE}/api/live/agents"; echo

echo
echo "== GET /api/live/conversations (seeded 'Demo chat') =="
CONVS=$(curl -fs "${BASE}/api/live/conversations")
echo "$CONVS"
CID=$(echo "$CONVS" | grep -o '"id":"[a-f0-9]*"' | head -1 | cut -d'"' -f4)
echo "conversation id = $CID"

echo
echo "== GET seeded conversation messages (before) =="
curl -fs "${BASE}/api/live/conversations/${CID}/messages"; echo

echo
echo "== POST a prompt to the conversation =="
curl -fs -X POST "${BASE}/api/live/conversations/${CID}/messages" \
  -H 'content-type: application/json' \
  -d '{"body":"Hello from the e2e test"}'; echo

echo
echo "== poll for the agent echo reply =="
REPLY=""
for i in $(seq 1 40); do
  MSGS=$(curl -fs "${BASE}/api/live/conversations/${CID}/messages")
  if echo "$MSGS" | grep -q 'You said:'; then
    REPLY="$MSGS"
    break
  fi
  sleep 0.5
done

echo "$REPLY"
echo
if echo "$REPLY" | grep -q '"role":"agent"' && echo "$REPLY" | grep -q 'You said:'; then
  echo "PASS: agent reply containing 'You said:' received — server-driven echo loop works."
  exit 0
else
  echo "FAIL: no agent reply with 'You said:' observed."
  echo "----- server log -----"; cat /tmp/live-example.log
  exit 1
fi
