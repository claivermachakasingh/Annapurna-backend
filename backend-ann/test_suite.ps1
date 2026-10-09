# ==============================================================================
# Annapurna Backend — Automated End-to-End Verification Test Suite
# Usage: powershell -ExecutionPolicy Bypass -File .\test_suite.ps1
# ==============================================================================

$ErrorActionPreference = "Stop"

Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "         ANNAPURNA BACKEND END-TO-END TEST SUITE          " -ForegroundColor Cyan
Write-Host "==========================================================" -ForegroundColor Cyan

# 1. Health check
$health = Invoke-RestMethod -Uri "http://localhost:8000/health"
if ($health.status -ne "ok") { 
    throw "Health check failed: expected status 'ok', got '$($health.status)'" 
}
Write-Host "[1/10] GET /health                      => PASS (Status: $($health.status))" -ForegroundColor Green

# 2. Ready check
$ready = Invoke-RestMethod -Uri "http://localhost:8000/ready"
if ($ready.status -ne "ready" -or -not $ready.database) { 
    throw "Ready check failed: DB must be connected and status must be 'ready'" 
}
Write-Host "[2/10] GET /ready                       => PASS (DB: $($ready.database), Redis: $($ready.redis))" -ForegroundColor Green

# 3. Login
$loginResp = Invoke-RestMethod -Method POST -Uri "http://localhost:8000/api/v1/auth/login" `
    -ContentType "application/json" `
    -Body '{"email":"kitchen@example.com","password":"demo123"}'
$token = $loginResp.access_token
if ([string]::IsNullOrWhiteSpace($token)) { 
    throw "Login failed: access_token is empty or invalid" 
}
Write-Host "[3/10] POST /api/v1/auth/login          => PASS (JWT Token Acquired)" -ForegroundColor Green

# 4. Auth Me
$me = Invoke-RestMethod -Uri "http://localhost:8000/api/v1/auth/me" `
    -Headers @{Authorization="Bearer $token"}
if ($me.role -ne "KITCHEN" -or [string]::IsNullOrWhiteSpace($me.id)) { 
    throw "Auth Me failed: expected role 'KITCHEN' and valid UUID, got role='$($me.role)'" 
}
Write-Host "[4/10] GET /api/v1/auth/me              => PASS (User: $($me.email), ID: $($me.id), Role: $($me.role))" -ForegroundColor Green

# 5. Create Surplus Batch 1
$batch1Payload = @{
    food_name     = "Dal Tadka"
    food_category = "cooked"
    quantity_kg   = 15.0
    expiry_at     = (Get-Date).AddHours(6).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
    prepared_at   = (Get-Date).AddHours(-1).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
} | ConvertTo-Json

$batch1 = Invoke-RestMethod -Method POST -Uri "http://localhost:8000/api/v1/surplus" `
    -Headers @{Authorization="Bearer $token"} `
    -ContentType "application/json" `
    -Body $batch1Payload
$batch1Id = $batch1.id
if ([string]::IsNullOrWhiteSpace($batch1Id) -or $batch1.status -ne "PENDING_SAFETY") { 
    throw "Create Surplus failed: expected status 'PENDING_SAFETY', got '$($batch1.status)'" 
}
Write-Host "[5/10] POST /api/v1/surplus (Batch 1)   => PASS (ID: $batch1Id, Status: $($batch1.status))" -ForegroundColor Green

# 6. Quality Check on Batch 1 (Real multipart form upload with 1x1 PNG image)
$pngBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
$sampleImg = "$env:TEMP\sample_food.png"
[IO.File]::WriteAllBytes($sampleImg, [Convert]::FromBase64String($pngBase64))

$qualityRaw = curl.exe -s -X POST "http://localhost:8000/api/v1/quality/check" `
    -H "Authorization: Bearer $token" `
    -F "batch_id=$batch1Id" `
    -F "temperature_c=65.0" `
    -F "image=@$sampleImg;type=image/png"

$qualityResp = $qualityRaw | ConvertFrom-Json
if ($null -eq $qualityResp -or ($qualityResp.visual.status -ne "GOOD" -and $qualityResp.quality_status -ne "GOOD")) {
    throw "Quality check failed: expected visual status 'GOOD', got response: $qualityRaw"
}
Write-Host "[6/10] POST /api/v1/quality/check       => PASS (Visual: $($qualityResp.visual.status))" -ForegroundColor Green

# 7. Approve Batch 1 (Human Inspection)
$approveResp = Invoke-RestMethod -Method POST -Uri "http://localhost:8000/api/v1/surplus/$batch1Id/approve" `
    -Headers @{Authorization="Bearer $token"} `
    -ContentType "application/json" `
    -Body '{"decision":"APPROVE","note":"Quality check passed, hot food approved"}'
if ($approveResp.status -ne "AVAILABLE" -and $approveResp.status -ne "APPROVED") {
    throw "Approve failed: expected status 'AVAILABLE', got '$($approveResp.status)'"
}
Write-Host "[7/10] POST /api/v1/surplus/{id}/approve => PASS (Status: $($approveResp.status))" -ForegroundColor Green

# Verify Batch 1 detail: Status must be AVAILABLE and approved_by must match logged-in user
$b1Detail = Invoke-RestMethod -Uri "http://localhost:8000/api/v1/surplus/$batch1Id" `
    -Headers @{Authorization="Bearer $token"}
if ($b1Detail.status -ne "AVAILABLE") { 
    throw "Batch 1 detail verification failed: expected status 'AVAILABLE', got '$($b1Detail.status)'" 
}
if ($b1Detail.approved_by -ne $me.id) { 
    throw "Batch 1 detail verification failed: approved_by ($($b1Detail.approved_by)) does not match user ID ($($me.id))" 
}
Write-Host "       Verification: Batch 1 Detail     => Status: $($b1Detail.status) (ApprovedBy: $($b1Detail.approved_by))" -ForegroundColor Yellow

# 8. Create Surplus Batch 2 and Divert
$batch2Payload = @{
    food_name     = "Stale Salad"
    food_category = "raw"
    quantity_kg   = 5.0
    expiry_at     = (Get-Date).AddHours(2).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
    prepared_at   = (Get-Date).AddHours(-5).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
} | ConvertTo-Json

$batch2 = Invoke-RestMethod -Method POST -Uri "http://localhost:8000/api/v1/surplus" `
    -Headers @{Authorization="Bearer $token"} `
    -ContentType "application/json" `
    -Body $batch2Payload
$batch2Id = $batch2.id

$divertResp = Invoke-RestMethod -Method POST -Uri "http://localhost:8000/api/v1/surplus/$batch2Id/divert" `
    -Headers @{Authorization="Bearer $token"}
if ($divertResp.status -ne "DIVERTED") { 
    throw "Divert failed: expected status 'DIVERTED', got '$($divertResp.status)'" 
}
Write-Host "[8/10] POST /api/v1/surplus/{id}/divert  => PASS (Diverted ID: $batch2Id, Status: $($divertResp.status))" -ForegroundColor Green

# 9. List All Surplus Batches
$listResp = Invoke-RestMethod -Uri "http://localhost:8000/api/v1/surplus" `
    -Headers @{Authorization="Bearer $token"}
if ($listResp.items.Count -lt 1) { 
    throw "List surplus failed: items list is empty" 
}
Write-Host "[9/10] GET /api/v1/surplus (List)        => PASS (Found $($listResp.items.Count) batches total)" -ForegroundColor Green

# 10. Test Offline Batch Sync Replay (Using kind='surplus' which persists to DB)
$syncBatchId = [System.Guid]::NewGuid().ToString()
$syncPayload = @{
    items = @(
        @{
            client_event_id = $syncBatchId
            kind            = "surplus"
            payload         = @{
                food_name     = "Sync Khichdi"
                food_category = "cooked"
                quantity_kg   = 8.0
                expiry_at     = (Get-Date).AddHours(5).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
                prepared_at   = (Get-Date).AddHours(-1).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
            }
        }
    )
} | ConvertTo-Json -Depth 5

$syncResp = Invoke-RestMethod -Method POST -Uri "http://localhost:8000/api/v1/sync/batch" `
    -Headers @{Authorization="Bearer $token"} `
    -ContentType "application/json" `
    -Body $syncPayload

if ($syncResp.results.Count -lt 1 -or $syncResp.results[0].status -ne "ACCEPTED") {
    throw "Sync batch failed: expected result status 'ACCEPTED', got: $($syncResp.results[0].status)"
}
Write-Host "[10/10] POST /api/v1/sync/batch          => PASS (Applied: $($syncResp.applied), Result: $($syncResp.results[0].status))" -ForegroundColor Green

Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "          ALL 10 END-TO-END TESTS PASSED 100%!            " -ForegroundColor Green
Write-Host "==========================================================" -ForegroundColor Cyan
