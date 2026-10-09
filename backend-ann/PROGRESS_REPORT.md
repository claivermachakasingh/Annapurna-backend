# Project Annapurna — Progress Report
**System:** High-Performance Go Backend, Real-Time Surplus Food Redistribution & Quality Assurance Engine  
**Project Reference:** SIH26234 (Ministry of Food Processing Industries / Food Waste Mitigation)  
**Status:** Core Backend Operational, Schema Aligned, Authenticated API Workflows 100% Verified  

---

## 1. Our Goal & Vision

The goal of **Annapurna** is to build a reliable, institutional food redistribution network that eliminates food waste in commercial and educational kitchens. The platform guarantees that:
1. Surplus food is certified safe before any donation occurs.
2. The redistribution process is completely automated, fair, and fast enough to deliver hot food within safe consumption deadlines.
3. Every handoff is recorded immutably to eliminate legal liability and ensure donor confidence.
4. Field workers can operate seamlessly even in low-connectivity rural or basement locations.

---

## 2. What Is Done (Completed & Tested)

### A. Infrastructure & Database Engine
- [x] **PostgreSQL 16 with PostGIS:** Containerized database with spatial extensions for geospatial proximity calculations.
- [x] **Redis 7 In-Memory Engine:** Dual-database structure (DB 0 for rate-limiting, locks, and SSE pub/sub; DB 1 for Asynq queues).
- [x] **Database Schema & Migrations Aligned:**
  - Designed and configured relational tables: `organizations`, `users`, `kitchens`, `surplus_batches`, `quality_checks`, `audit_log`, `qr_events`, `distribution_offers`, `routes`.
  - Added required columns: `batch_code`, `food_name`, `meal_id`, `food_category`, `prepared_at`, `expiry_at`.
  - Verified and aligned `quality_checks` table with visual inspection status, confidence score, danger zone duration, and safety decision fields.
  - Configured foreign-key constraints linking batches to registered kitchens.
- [x] **High-Performance Connection Pooling:** Implemented `pgxpool` with automated reconnection handling.

### B. Authentication, Security & RBAC
- [x] **Database Credential Authentication:** Real PostgreSQL authentication using salted Bcrypt password hashing.
- [x] **JWT Access Token Issuance:** Signed HS256 tokens carrying user UUID, organization UUID, and role claims (`KITCHEN`, `NGO`, `ADMIN`, `LOGISTICS`).
- [x] **Identity Verification (`/api/v1/auth/me`):** Authenticated endpoint returning user profile and role.
- [x] **Seed Credentials Verified:** Pre-seeded demo accounts operational (`kitchen@example.com` / `demo123`).

### C. Surplus Batch Lifecycle State Machine
- [x] **Surplus Batch Creation (`POST /api/v1/surplus`):** Inserts batch records into PostgreSQL with dynamic UUID parsing from token claims.
- [x] **Surplus Listing (`GET /api/v1/surplus`):** Returns active batches with role-based multi-tenant scoping.
- [x] **Surplus Detail (`GET /api/v1/surplus/{id}`):** Retrieves batch details, expiration deadlines, and approval history.
- [x] **Food Safety Quality Check (`POST /api/v1/quality/check`):** Multi-factor evaluation combining food image analysis and temperature telemetry.
- [x] **Human Inspection Approval (`POST /api/v1/surplus/{id}/approve`):** Human review transition advancing food status from `PENDING_SAFETY` to `AVAILABLE`, generating cryptographic SHA-256 audit proof.
- [x] **Food Diversion (`POST /api/v1/surplus/{id}/divert`):** Transitions unsafe food to `DIVERTED` for composting or biogas.

### D. Offline-First Synchronization & Background Architecture
- [x] **Batch Synchronization (`POST /api/v1/sync/batch`):** Replays offline field transactions with client UUIDv7 deduplication and FIFO order.
- [x] **Real-Time SSE Event Hub (`GET /api/v1/events/stream`):** Live Server-Sent Events hub broadcasting status changes and alerts.
- [x] **Asynq Worker Skeleton (`cmd/worker/main.go`):** Background task worker configured with prioritized queues (`critical`, `default`, `low`).
- [x] **Health & Monitoring Probes:** Operational `/health`, `/ready`, and `/metrics` (Prometheus) endpoints.
- [x] **Automated Test Suite:** Created `test_suite.ps1` running 10 comprehensive tests verifying all core workflows live.

---

## 3. What Is Left (Roadmap & Remaining Work)

| Milestone / Module | Technical Description | Priority |
|---|---|---|
| **Geospatial Matching Engine** | Implement the PostGIS nearest-neighbor radius query to match `AVAILABLE` food with candidate NGOs based on travel distance, capacity, shelf-life slack, and 48-hour fairness penalty. | **High** |
| **Vehicle Routing Problem (VRP)** | Connect to the Python OR-Tools sidecar (`/v1/build-route`) to generate multi-stop vehicle route plans with time windows for logistics drivers. | **High** |
| **Active Background Crons** | Enable the 1-minute recurring cron job for `TaskExpirySweep` (flagging expired food under distributed Redis locks) and 30-minute offer timeout checks. | **Medium** |
| **FCM Push Notification Dispatch** | Wire Firebase Admin SDK credentials in `internal/pushx` for instant mobile push delivery when new surplus matches are generated. | **Medium** |
| **ESG PDF Report Generator** | Implement `maroto/v2` PDF generation service to produce downloadable ESG compliance certificates and sustainability analytics. | **Medium** |
| **Flutter Mobile App Integration** | Connect the Flutter client (Kitchen, NGO, Driver, Admin) to the live Go API endpoints and validate offline outbox replay from physical devices. | **Medium** |
| **Rate Limiting & Security Hardening** | Implement Redis token-bucket rate limiter per IP/client token and enforce minimum app version checks via `X-App-Version`. | **Low** |
