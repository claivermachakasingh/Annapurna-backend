# Project Annapurna — AI-Powered Food Waste Mitigation & Redistribution Platform
**Problem Statement Reference:** SIH26234 | Ministry of Food Processing Industries (MoFPI)  

---

## 1. Problem Statement (PS) Context & Real-World Challenge

In large-scale institutional dining operations — such as university campus messes, corporate cafeterias, hospital kitchens, and event caterers — massive quantities of freshly prepared, highly nutritious food are discarded every day. 

### Why this happens today:
1. **Demand Forecasting Errors:** Kitchen managers rely on static manual headcount estimates rather than dynamic demand signals, resulting in systematic daily overproduction (15–30% surplus).
2. **Food Safety Uncertainty & Liability Fears:** Kitchens discard safe, surplus food because they lack an objective, verifiable mechanism to prove that food stored for several hours has remained outside the danger temperature zone (5°C to 60°C).
3. **Logistics & Matching Friction:** Connecting perishable surplus to NGOs is done via frantic phone calls. Without real-time geospatial proximity matching, food spoils before it can be collected and transported.
4. **Lack of Custody & Traceability:** No tamper-evident record exists to verify who prepared, transported, and received food, creating hesitation among donors and recipients.
5. **Connectivity Blackouts in Field Ops:** Kitchen basements and rural NGO drop-off points frequently lack internet connectivity, causing manual tracking to break down.

---

## 2. What We Are Building

**Annapurna** is a high-throughput, mobile-first surplus food redistribution platform that stops waste at the source and connects edible food to nearby shelters before it spoils.

### Core Capabilities:
- **Pre-Cooking Demand Prediction:** Forecasts meal consumption curves using quantile regression to minimize initial overproduction.
- **AI & Computer Vision Food Quality Assurance:** Evaluates food freshness using computer vision combined with IoT temperature sensor telemetry to ensure only safe food reaches beneficiaries.
- **Fair Geospatial Matching:** Automatically matches available surplus with nearby verified NGOs based on travel-time windows, recipient capacity, and 48-hour historical fairness balance.
- **Cryptographic SHA-256 Custody Chain:** Generates a tamper-evident QR audit chain for every physical handoff (Kitchen $\rightarrow$ Transporter $\rightarrow$ NGO).
- **Secondary Recovery Hierarchy:** Automatically diverts food unsuitable for direct human consumption to animal feed, composting, or biogas production.
- **Offline-First Synchronization:** Allows kitchen and logistics workers to scan QR codes and log events in airplane mode, automatically syncing with guaranteed deduplication upon reconnection.

---

## 3. Technology Stack & Architecture Justifications

| Technology | Role | Technical Justification |
|---|---|---|
| **Go 1.23+ (Chi Router)** | Core REST API Backend | **Why Go?** Extreme throughput, minimal memory footprint (~25 MB RSS), and zero GC pauses on critical paths. Unlike Python or Node.js web frameworks, Go provides compile-time type safety and native high-concurrency goroutines ideal for high-frequency IoT and mobile sync I/O. |
| **PostgreSQL 16 + PostGIS** | Primary Relational & Spatial Database | **Why Postgres + PostGIS?** Strict ACID guarantees prevent race conditions when multiple NGOs claim the same surplus batch. PostGIS provides native spatial spherical indexing (`ST_DWithin`, `ST_DistanceSphere`), enabling sub-millisecond proximity queries between kitchens and shelters. |
| **Redis 7** | Cache, Locks & Message Broker | **Why Redis?** Dual-database configuration: <br>• **DB 0:** Sub-millisecond session caching, distributed locks (preventing double-claims during matching), and Server-Sent Events (SSE) pub/sub.<br>• **DB 1:** Storage backend for asynchronous job queues. |
| **Asynq** | Distributed Background Task Queue | **Why Asynq?** Reliable, Redis-backed asynchronous worker system with priority queues (`critical`, `default`, `low`). Offloads non-blocking workloads (1-minute automated food expiry sweeps, offer timeouts, push notifications) away from the HTTP request thread. |
| **SHA-256 QR Custody Chain** | Tamper-Evident Proof Engine | **Why custom cryptographic chaining?** Each handoff event hashes the prior event's hash, actor ID, batch UUID, timestamp, and sensory evidence ($H_n = \text{SHA256}(H_{n-1} \parallel \text{data})$). If any record is altered in the database, the cryptographic chain breaks, providing instant proof of tampering without the cost or latency of a public blockchain. |
| **Offline-First Outbox Pattern** | Field Resiliency Engine | **Why an offline outbox?** Kitchen basements and delivery drop-offs often have zero network. The system assigns client UUIDv7 IDs and replays queued transactions sequentially on the `/api/v1/sync/batch` endpoint with idempotent Redis deduplication. |
| **Docker & Docker Compose** | Infrastructure Virtualization | Provides a one-command reproducible local environment ensuring identical PostgreSQL and Redis configurations across development, testing, and production. |
