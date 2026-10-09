#!/usr/bin/env bash
# ─── Annapurna end-to-end smoke test ──────────────────────────────────────────
# Exercises the live HTTP surface against a running server and a seeded DB.
#
#   ./scripts/smoke.sh                                  # defaults to :8080
#   BASE_URL=http://localhost:8080 ./scripts/smoke.sh
#
# Requires: curl, python3. Exit code 0 = every check passed.
set -uo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
API="/api/v1"   # path prefix only — req() prepends $BASE_URL
EMAIL="${SMOKE_EMAIL:-kitchen@example.com}"
PASSWORD="${SMOKE_PASSWORD:-demo123}"

PASS=0
FAIL=0
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

c_ok()   { printf '  \033[32mPASS\033[0m %s\n' "$1"; PASS=$((PASS + 1)); }
c_bad()  { printf '  \033[31mFAIL\033[0m %s\n' "$1"; FAIL=$((FAIL + 1)); }
check()  { if [ "$2" = "$3" ]; then c_ok "$1"; else c_bad "$1 (got '$2', want '$3')"; fi; }
section(){ printf '\n\033[1m%s\033[0m\n' "$1"; }

jget() { python3 -c "import sys,json;d=json.load(sys.stdin);
import functools
p='$1'.split('.')
v=d
for k in p:
    if k=='': continue
    v=v[int(k)] if isinstance(v,list) else v[k]
print(v if not isinstance(v,(dict,list)) else json.dumps(v))" 2>/dev/null; }

# curl wrapper: prints "<http_code> <body-file>"
req() { # method path [token] [data] [extra curl args...]
  local method="$1" path="$2" token="${3:-}" data="${4:-}"
  # drop the four named args, keep any curl extras (-F, -H, ...)
  if [ "$#" -ge 4 ]; then shift 4; else shift $#; fi
  local out="$TMP/body" code
  local -a args=(-sS -m 20 -o "$out" -w '%{http_code}' -X "$method" "$BASE_URL$path")
  [ -n "$token" ] && args+=(-H "Authorization: Bearer $token")
  if [ -n "$data" ]; then args+=(-H 'Content-Type: application/json' -d "$data"); fi
  [ "$#" -gt 0 ] && args+=("$@")
  code="$(curl "${args[@]}")" || code="000"
  printf '%s %s' "$code" "$out"
}

# ─── 0. infrastructure ────────────────────────────────────────────────────────
section "0. infrastructure"
read -r code body <<<"$(req GET /health)"
check "GET /health → 200" "$code" "200"
check "health status ok" "$(jget status <"$body")" "ok"

read -r code body <<<"$(req GET /ready)"
check "GET /ready → 200" "$code" "200"
check "postgres ready" "$(jget database <"$body")" "True"

# ─── 1. auth ──────────────────────────────────────────────────────────────────
section "1. auth"
read -r code body <<<"$(req POST "$API/auth/login" "" "{\"email\":\"$EMAIL\",\"password\":\"$PASSWORD\"}")"
check "POST /auth/login → 200" "$code" "200"
TOKEN="$(jget access_token <"$body")"
[ -n "$TOKEN" ] && c_ok "access token issued" || c_bad "access token missing"

read -r code body <<<"$(req GET "$API/auth/me" "$TOKEN")"
check "GET /auth/me → 200" "$code" "200"
check "role is KITCHEN" "$(jget role <"$body")" "KITCHEN"

read -r code _ <<<"$(req GET "$API/surplus" "")"
check "unauthenticated /surplus → 401" "$code" "401"

read -r code _ <<<"$(req POST "$API/auth/login" "" '{"email":"kitchen@example.com","password":"wrong-password"}')"
check "bad password → 401" "$code" "401"

# ─── 1b. rotating refresh tokens (D22) ───────────────────────────────────────
read -r code body <<<"$(req POST "$API/auth/login" "" "{\"email\":\"$EMAIL\",\"password\":\"$PASSWORD\"}")"
REFRESH1="$(jget refresh_token <"$body")"
[ -n "$REFRESH1" ] && c_ok "refresh token issued on login" || c_bad "no refresh token on login"

read -r code body <<<"$(req POST "$API/auth/refresh" "" "{\"refresh_token\":\"$REFRESH1\"}")"
check "POST /auth/refresh → 200" "$code" "200"
REFRESH2="$(jget refresh_token <"$body")"
ACCESS2="$(jget access_token <"$body")"
[ -n "$REFRESH2" ] && [ "$REFRESH2" != "$REFRESH1" ] && c_ok "refresh token rotated" || c_bad "refresh token was not rotated"

read -r code body <<<"$(req GET "$API/auth/me" "$ACCESS2")"
check "rotated access token is a real user" "$(jget role <"$body")" "KITCHEN"
check "rotated token subject is the login user" "$(jget email <"$body")" "$EMAIL"

read -r code body <<<"$(req POST "$API/auth/refresh" "" "{\"refresh_token\":\"$REFRESH1\"}")"
check "replaying a rotated token → 401" "$code" "401"
check "code REFRESH_TOKEN_REUSED" "$(jget code <"$body")" "REFRESH_TOKEN_REUSED"

read -r code _ <<<"$(req POST "$API/auth/logout" "" "{\"refresh_token\":\"$REFRESH2\"}")"
check "POST /auth/logout → 204" "$code" "204"
read -r code _ <<<"$(req POST "$API/auth/refresh" "" "{\"refresh_token\":\"$REFRESH2\"}")"
check "refresh after logout → 401" "$code" "401"

# ─── 2. surplus creation ─────────────────────────────────────────────────────
section "2. surplus lifecycle"
NOW_ISO="$(python3 -c 'import datetime;print(datetime.datetime.now(datetime.UTC).strftime("%Y-%m-%dT%H:%M:%SZ"))')"
EXP_ISO="$(python3 -c 'import datetime;print((datetime.datetime.now(datetime.UTC)+datetime.timedelta(hours=4)).strftime("%Y-%m-%dT%H:%M:%SZ"))')"
PREP_ISO="$(python3 -c 'import datetime;print((datetime.datetime.now(datetime.UTC)-datetime.timedelta(minutes=30)).strftime("%Y-%m-%dT%H:%M:%SZ"))')"

CREATE="{\"food_name\":\"Smoke Test Khichdi\",\"food_category\":\"cooked\",\"quantity_kg\":9.5,\"prepared_at\":\"$PREP_ISO\",\"expiry_at\":\"$EXP_ISO\"}"
read -r code body <<<"$(req POST "$API/surplus" "$TOKEN" "$CREATE")"
check "POST /surplus → 201" "$code" "201"
BATCH="$(jget id <"$body")"
check "new batch is PENDING_SAFETY" "$(jget status <"$body")" "PENDING_SAFETY"
check "safety_status pending" "$(jget safety_status <"$body")" "PENDING"

read -r code _ <<<"$(req POST "$API/surplus" "$TOKEN" '{"food_name":"","quantity_kg":-2}')"
check "invalid surplus payload → 4xx" "$(case $code in 4*) echo 4xx;; *) echo "$code";; esac)" "4xx"

# ─── 3. quality check (image upload) ─────────────────────────────────────────
section "3. quality verification"
python3 - "$TMP/good.png" "$TMP/bad.jpg" <<'PY'
import struct, sys, zlib
def png(path, w=64, h=64):
    raw = b''
    for y in range(h):
        raw += b'\x00' + b''.join(bytes([(x + y) % 256, (x * 3) % 256, 128]) for x in range(w))
    def chunk(t, d):
        return struct.pack('>I', len(d)) + t + d + struct.pack('>I', zlib.crc32(t + d) & 0xffffffff)
    ihdr = struct.pack('>IIBBBBB', w, h, 8, 2, 0, 0, 0)
    with open(path, 'wb') as f:
        f.write(b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', ihdr) + chunk(b'IDAT', zlib.compress(raw)) + chunk(b'IEND', b''))
png(sys.argv[1])
open(sys.argv[2], 'wb').write(b'this is not an image at all, just text bytes')
PY

read -r code body <<<"$(req POST "$API/quality/check" "$TOKEN" "" -F "batch_id=$BATCH" -F "image=@$TMP/good.png" -F 'device_preview=0.91')"
check "POST /quality/check → 200" "$code" "200"
QC_DECISION="$(jget safety_decision.status <"$body")"
check "safety decision is ELIGIBLE" "$QC_DECISION" "ELIGIBLE"
check "visual verdict GOOD" "$(jget visual.status <"$body")" "GOOD"
IMG_PATH="$(jget image_path <"$body")"
if [ -n "$IMG_PATH" ] && [ -f "$IMG_PATH" ]; then c_ok "upload re-encoded to disk ($(basename "$IMG_PATH"))"; else c_bad "upload path missing on disk: '$IMG_PATH'"; fi

read -r code body <<<"$(req POST "$API/quality/check" "$TOKEN" "" -F "image=@$TMP/good.png")"
check "quality check without batch_id → 422" "$code" "422"

read -r code body <<<"$(req POST "$API/quality/check" "$TOKEN" "" -F "batch_id=$BATCH" -F "image=@$TMP/bad.jpg")"
check "non-image upload → 415" "$code" "415"
check "error code IMAGE_UNSUPPORTED" "$(jget code <"$body")" "IMAGE_UNSUPPORTED"

# ─── 4. approval + QR chain ──────────────────────────────────────────────────
section "4. approval & QR audit chain"
read -r code body <<<"$(req POST "$API/surplus/$BATCH/approve" "$TOKEN" '{"decision":"APPROVE","notes":"smoke test"}')"
check "POST approve → 200" "$code" "200"
check "batch became AVAILABLE" "$(jget status <"$body")" "AVAILABLE"
check "safety_status ELIGIBLE" "$(jget safety_status <"$body")" "ELIGIBLE"

read -r code body <<<"$(req GET "$API/qr/$BATCH" "$TOKEN")"
check "GET /qr/{id} → 200" "$code" "200"
check "chain valid" "$(jget chain_valid <"$body")" "True"
EVENTS="$(jget event_count <"$body")"
if [ "${EVENTS:-0}" -ge 2 ] 2>/dev/null; then c_ok "timeline has CREATED + APPROVED ($EVENTS events)"; else c_bad "expected ≥2 QR events, got '$EVENTS'"; fi

read -r code body <<<"$(req GET "$API/qr/$BATCH/verify" "$TOKEN")"
check "GET /qr/{id}/verify → 200" "$code" "200"
check "verify says valid" "$(jget valid <"$body")" "True"

CEID="smoke-pickup-$$"
read -r code body <<<"$(req POST "$API/qr/$BATCH/event" "$TOKEN" "{\"event_type\":\"PICKED_UP\",\"lat\":19.076,\"lng\":72.8777,\"client_ts\":\"$NOW_ISO\",\"client_event_id\":\"$CEID\"}")"
check "append PICKED_UP → 201" "$code" "201"
EVENT_ID="$(jget id <"$body")"
read -r code body <<<"$(req POST "$API/qr/$BATCH/event" "$TOKEN" "{\"event_type\":\"PICKED_UP\",\"client_event_id\":\"$CEID\"}")"
check "client_event_id replay is idempotent" "$(jget id <"$body")" "$EVENT_ID"

read -r code _ <<<"$(req POST "$API/qr/$BATCH/event" "$TOKEN" '{"event_type":"NOT_AN_EVENT"}')"
check "unknown event_type → 422" "$code" "422"
read -r code _ <<<"$(req GET "$API/qr/00000000-0000-4000-8000-000000000999" "$TOKEN")"
check "unknown batch timeline → 404" "$code" "404"

# ─── 5. diversion ────────────────────────────────────────────────────────────
section "5. diversion"
read -r code body <<<"$(req POST "$API/surplus/$BATCH/divert" "$TOKEN" '{"stream":"COMPOST","quantity_kg":9.5,"reason":"not collected in time"}')"
check "POST divert → 200" "$code" "200"
read -r code body <<<"$(req GET "$API/surplus/$BATCH" "$TOKEN")"
check "batch is DIVERTED" "$(jget status <"$body")" "DIVERTED"
read -r code body <<<"$(req POST "$API/surplus/$BATCH/divert" "$TOKEN" '{"stream":"BIOGAS"}')"
check "double divert → 409" "$code" "409"
read -r code body <<<"$(req POST "$API/surplus/$BATCH/divert" "$TOKEN" '{"stream":"LANDFILL"}')"
check "invalid stream → 409" "$code" "409"

# safety-rejected batches can never be approved
read -r _ body <<<"$(req GET "$API/surplus?limit=50" "$TOKEN")"
REJECTED_ID="$(python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)
except Exception:
    sys.exit()
for b in (d.get('items') or []):
    if b.get('safety_status') == 'REJECTED':
        print(b['id']); break" <"$body" 2>/dev/null || true)"
if [ -n "$REJECTED_ID" ] && [ "$REJECTED_ID" != "None" ]; then
  read -r code body <<<"$(req POST "$API/surplus/$REJECTED_ID/approve" "$TOKEN" '{"decision":"APPROVE"}')"
  check "approving a safety-rejected batch → 409" "$code" "409"
  REJ_CODE="$(jget code <"$body")"
  if [ "$REJ_CODE" = "SAFETY_REJECTED_CANNOT_APPROVE" ] || [ "$REJ_CODE" = "INVALID_STATE_TRANSITION" ]; then
    c_ok "rejected batch cannot be approved ($REJ_CODE)"
  else
    c_bad "unexpected code for rejected approval: '$REJ_CODE'"
  fi
else
  printf '  \033[33mSKIP\033[0m no REJECTED batch in seed data\n'
fi

# ─── 5b. SSE stream ──────────────────────────────────────────────────────────
section "5b. live event stream"
SSE_OUT="$(curl -sS -m 3 -N "$BASE_URL$API/events/stream?token=$TOKEN" 2>/dev/null || true)"
if printf '%s' "$SSE_OUT" | grep -q "kitchen_id.:.00000000-0000-4000-8000-000000000101"; then
  c_ok "SSE stream is scoped to the token's kitchen"
else
  c_bad "SSE stream did not report the caller's kitchen"
fi
read -r code _ <<<"$(req GET "$API/events/stream?token=not-a-token" "")"
check "SSE with a bad token → 401" "$code" "401"

# ─── 6. offline sync replay ──────────────────────────────────────────────────
section "6. offline sync (dedupe)"
SYNC_CEID="smoke-sync-$$"
SYNC_PAYLOAD="{\"items\":[{\"client_event_id\":\"$SYNC_CEID\",\"kind\":\"surplus\",\"client_ts\":\"$NOW_ISO\",\"payload\":{\"food_name\":\"Smoke Offline Poha\",\"food_category\":\"cooked\",\"quantity_kg\":4.5,\"prepared_at\":\"$PREP_ISO\",\"expiry_at\":\"$EXP_ISO\"}}]}"
read -r code body <<<"$(req POST "$API/sync/batch" "$TOKEN" "$SYNC_PAYLOAD")"
check "POST /sync/batch → 200" "$code" "200"
check "first replay ACCEPTED" "$(jget results.0.status <"$body")" "ACCEPTED"
read -r code body <<<"$(req POST "$API/sync/batch" "$TOKEN" "$SYNC_PAYLOAD")"
check "second replay DUPLICATE" "$(jget results.0.status <"$body")" "DUPLICATE"
check "duplicates counter = 1" "$(jget duplicates <"$body")" "1"

read -r code _ <<<"$(req POST "$API/sync/batch" "$TOKEN" '{"items":[]}')"
check "empty sync batch → 422" "$code" "422"

# ─── summary ─────────────────────────────────────────────────────────────────
printf '\n──────────────────────────────────────────────\n'
printf '  %d passed, %d failed\n' "$PASS" "$FAIL"
printf '──────────────────────────────────────────────\n'
[ "$FAIL" -eq 0 ]
