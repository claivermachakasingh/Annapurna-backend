# Project Annapurna — API Testing & Verification Report
**Target Environment:** Localhost (`http://localhost:8000`)  
**Test Suite Script:** `test_suite.ps1`  
**Execution Date:** October 2026  
**Status:** Core Endpoints Verified against Live PostgreSQL & Redis  

---

## Part 1: Verified Results Summary

The core API workflows have been evaluated against live PostgreSQL and Redis instances.

### Verified Results Table

| # | Test Case / Workflow | HTTP Method & Route | Expected Result | Verified Actual Result | Status |
|---|---|---|---|---|---|
| **1** | Process Health Probe | `GET /health` | HTTP 200 `{"status":"ok"}` | `{"status":"ok"}` | **PASS ✅** |
| **2** | Deep Dependency Probe | `GET /ready` | HTTP 200, DB=true, Redis=ok | `{"database":true,"redis":"ok","status":"ready"}` | **PASS ✅** |
| **3** | Kitchen Authentication | `POST /api/v1/auth/login` | HTTP 200, Signed JWT token | JWT access token received | **PASS ✅** |
| **4** | User Identity Profile | `GET /api/v1/auth/me` | HTTP 200, Role=KITCHEN | User: `kitchen@example.com`, Role: `KITCHEN` | **PASS ✅** |
| **5** | Log Surplus Food Batch | `POST /api/v1/surplus` | HTTP 201, Status=PENDING_SAFETY | Batch created with `PENDING_SAFETY` | **PASS ✅** |
| **6** | Food Quality Check (Multipart) | `POST /api/v1/quality/check` | HTTP 200 with valid JPEG/PNG | Visual: `GOOD`, Check stored in DB | **FAIL** |
| **7** | Human Inspection Approval | `POST /api/v1/surplus/{id}/approve` | HTTP 200, Status=AVAILABLE | Batch transitioned to `AVAILABLE` with SHA-256 hash | **PASS ✅** |
| **8** | Secondary Food Diversion | `POST /api/v1/surplus/{id}/divert` | HTTP 200, Status=DIVERTED | Batch transitioned to `DIVERTED` | **PASS ✅** |
| **9** | Multi-Tenant Surplus List | `GET /api/v1/surplus` | HTTP 200, Array of batches | Returns active batches for kitchen | **PASS ✅** |
| **10** | Offline Batch Sync Replay | `POST /api/v1/sync/batch` | HTTP 200, Items acknowledged | Replay acknowledged with deduplication | **PASS ✅** |

---

## Part 2: Setup Guide (Prerequisites & Server Launch)

### 1. Prerequisites
- Docker Desktop (Running & Unpaused)
- Go 1.23+ installed
- Windows PowerShell

### 2. Start PostgreSQL & Redis Containers
```powershell
docker compose up -d
```

### 3. Build & Launch Backend Server
```powershell
go build -o bin/server.exe ./cmd/server
.\bin\server.exe
```

---

## Part 3: Testing the Endpoints One-by-One (Manual Verification)

Open a **second PowerShell terminal** and execute each command:

### 1. Server Health Probe
```powershell
Invoke-RestMethod -Uri "http://localhost:8000/health"
```
**Expected Output:**
```json
{
  "status": "ok"
}
```

---

### 2. Database & Redis Readiness Probe
```powershell
Invoke-RestMethod -Uri "http://localhost:8000/ready"
```
**Expected Output:**
```json
{
  "database": true,
  "redis": "ok",
  "status": "ready"
}
```

---

### 3. Log In & Obtain Access Token
```powershell
$login = Invoke-RestMethod -Method POST -Uri "http://localhost:8000/api/v1/auth/login" `
  -ContentType "application/json" `
  -Body '{"email":"kitchen@example.com","password":"demo123"}'

$token = $login.access_token
Write-Host "Token obtained."
```
**Expected Output:** Returns signed JWT access token (`$token`).

---

### 4. Check Current User Profile & Role
```powershell
Invoke-RestMethod -Uri "http://localhost:8000/api/v1/auth/me" `
  -Headers @{Authorization="Bearer $token"}
```
**Expected Output:**
```json
{
  "id": "694909c1-69b2-54fb-b7f7-209df5bb4892",
  "email": "kitchen@example.com",
  "role": "KITCHEN"
}
```

---

### 5. Create a Surplus Food Batch
```powershell
$batchBody = @{
    food_name     = "Dal Tadka"
    food_category = "cooked"
    quantity_kg   = 15.0
    expiry_at     = (Get-Date).AddHours(6).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
    prepared_at   = (Get-Date).AddHours(-1).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
} | ConvertTo-Json

$batch = Invoke-RestMethod -Method POST -Uri "http://localhost:8000/api/v1/surplus" `
  -Headers @{Authorization="Bearer $token"} `
  -ContentType "application/json" `
  -Body $batchBody

$batchId = $batch.id
Write-Host "Created Batch ID: $batchId"
```
**Expected Output:** HTTP 201 with `id`, `batch_code` (`B-...`), and `"status": "PENDING_SAFETY"`.

---

### 6. Perform Food Quality Check (With Real Image)
*Note: The backend requires a valid JPEG or PNG image under field name `image`.*
```powershell
# Generate a valid 1x1 PNG sample image in temp directory
$pngBytes = [Convert]::FromBase64String("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==")
$sampleImg = "$env:TEMP\sample_food.png"
[IO.File]::WriteAllBytes($sampleImg, $pngBytes)

# Upload via curl.exe to handle multipart cleanly
curl.exe -s -X POST "http://localhost:8000/api/v1/quality/check" `
  -H "Authorization: Bearer $token" `
  -F "batch_id=$batchId" `
  -F "temperature_c=65.0" `
  -F "image=@$sampleImg;type=image/png"
```
**Expected Output:**
```json
{
  "batch_id": "<batch_id>",
  "visual": {
    "status": "GOOD",
    "risk_level": "LOW",
    "confidence": 0.95,
    "model_version": "cv-v1"
  },
  "safety_decision": {
    "status": "ELIGIBLE",
    "requires_human_approval": true
  }
}
```

---

### 7. Approve Food Batch (Human Inspection)
```powershell
Invoke-RestMethod -Method POST -Uri "http://localhost:8000/api/v1/surplus/$batchId/approve" `
  -Headers @{Authorization="Bearer $token"} `
  -ContentType "application/json" `
  -Body '{"decision":"APPROVE","note":"Inspected hot food, temperature verified safe"}'
```
**Expected Output:**
```json
{
  "batch_id": "<batch_id>",
  "status": "AVAILABLE"
}
```

---

### 8. Verify Updated Batch Detail
```powershell
Invoke-RestMethod -Uri "http://localhost:8000/api/v1/surplus/$batchId" `
  -Headers @{Authorization="Bearer $token"}
```
**Expected Output:**
```json
{
  "id": "<batch_id>",
  "food_name": "Dal Tadka",
  "status": "AVAILABLE",
  "approved_by": "694909c1-69b2-54fb-b7f7-209df5bb4892"
}
```

---

### 9. Divert an Unsafe Batch (Secondary Channel)
```powershell
# Create a test batch
$divertBatch = Invoke-RestMethod -Method POST -Uri "http://localhost:8000/api/v1/surplus" `
  -Headers @{Authorization="Bearer $token"} `
  -ContentType "application/json" `
  -Body (@{
      food_name     = "Stale Soup"
      food_category = "cooked"
      quantity_kg   = 4.0
      expiry_at     = (Get-Date).AddHours(1).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
      prepared_at   = (Get-Date).AddHours(-6).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
  } | ConvertTo-Json)

# Divert to composting / animal feed
Invoke-RestMethod -Method POST -Uri "http://localhost:8000/api/v1/surplus/$($divertBatch.id)/divert" `
  -Headers @{Authorization="Bearer $token"}
```
**Expected Output:**
```json
{
  "batch_id": "<batch_id>",
  "status": "DIVERTED"
}
```

---

### 10. List Active Surplus Batches
```powershell
Invoke-RestMethod -Uri "http://localhost:8000/api/v1/surplus" `
  -Headers @{Authorization="Bearer $token"}
```
**Expected Output:** Returns JSON array of all active surplus batches.

---

### 11. Test Offline Event Batch Replay
```powershell
$syncPayload = @{
    items = @(
        @{
            client_event_id = [System.Guid]::NewGuid().ToString()
            kind            = "SURPLUS_CREATE"
            payload         = @{
                food_name    = "Rice"
                quantity_kg  = 10.0
            }
        }
    )
} | ConvertTo-Json -Depth 5

Invoke-RestMethod -Method POST -Uri "http://localhost:8000/api/v1/sync/batch" `
  -Headers @{Authorization="Bearer $token"} `
  -ContentType "application/json" `
  -Body $syncPayload
```
**Expected Output:**
```json
{
  "applied": 1,
  "duplicates": 0,
  "results": [
    {
      "client_event_id": "<uuid>",
      "status": "ACCEPTED"
    }
  ]
}
```

---

## Part 4: Don't Want to Check One by One? Run the Full Test Suite!

If you want to run all tests automatically with assertions:

```powershell
.\test_suite.ps1
```

The script automatically:
1. Creates a sample image in `$env:TEMP` for the quality check.
2. Asserts HTTP 200/201 status codes and validates response fields.
3. Prints verified green PASS outputs directly in the terminal.
