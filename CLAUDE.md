# CLAUDE.md

This file provides guidance to Claude Code when working with this repository.

---

## Problem Statement: The "Self-Healing" AIOps Dashboard

### The Core Challenge
We possess a dataset of **55,000 log entries** split across **18 VMware ESXi log files**. Currently, we can detect that anomalies exist, but we lack the **"Cure"**—the actionable intelligence to resolve them. We are seeing the symptoms (anomalies), but we are missing the diagnosis and the prescription.

### The Objective
Build a high-performance **AIOps Dashboard** that transforms these 55,000 raw log lines into a predictive resolution engine. The system must not just flag an error; it must explain it in simple English and provide a copy-paste fix.

---

## Functional Requirements

1. **High-Speed Ingestion:** The system must parse and aggregate all 18 files/55,000 rows in milliseconds.

2. **The "Cure" Engine (Local AI Integration):**
   - Integrate with **Local Ollama** instance
   - For every unique anomaly pattern detected, query the AI to generate:
     - **Root Cause:** A "Like I'm 5" explanation of the error
     - **The Fix:** A specific CLI command or config change to resolve it
     - **Predictive Downtime:** An estimation of how long the system will be down if ignored

3. **Visualization:** A clean, responsive dashboard (Dark Mode) to display the "Risk Score," "Error Distribution," and the "AI Recommendations."

---

## Efficiency Constraints (Crucial)

- **Deduplication Strategy:** You cannot send 55,000 requests to a local LLM; it will crash or take days. Implement a **Clustering Algorithm** (e.g., grouping by string similarity) to identify unique error *patterns* first, and only send those patterns to Ollama for analysis.

- **Tech Stack:**
  - **Backend:** Go (Golang) with Fiber for raw throughput
  - **Frontend:** Vanilla HTML/JS (no build steps, instant deployment)

---

## Definition of Done

The application launches, loads the 55k logs instantly, groups them into unique patterns, and populates a "Fix It" column using answers retrieved from the local Ollama model.

---

## Technology Stack

### Backend
- **Language:** Go 1.25.6
- **Framework:** Fiber v2.52 (ultra-fast HTTP framework)
- **WebSocket:** github.com/gofiber/contrib/websocket (real-time updates)
- **AI Integration:** Ollama (llama3.2 model)

### Frontend
- **No Framework:** Vanilla HTML/CSS/JavaScript
- **Charts:** ApexCharts
- **Animations:** GSAP (GreenSock)
- **Theme:** Dark mode with glassmorphism styling

### Data Source
- VMware ESXi diagnostic bundle: `esx-SGRL-ESX01-2026-01-10--09.39-2102690/`
- Primary log: `var/run/log/hostd.log` (55,703 lines)
- Additional VM logs: 18 `vmware.log` files throughout the bundle
- **Total: 85,082 log entries from 19 files**
- **Ingestion Time: ~614ms** (sub-second!)
- **Unique Patterns: 33,562 clusters** (deduplication ratio: 2.5x)

---

## Quick Start

```bash
# 1. Start Ollama (optional but recommended for AI features)
ollama serve &
ollama pull llama3.2

# 2. Run the dashboard
go run main.go

# 3. Open browser
open http://localhost:3000

# 4. Generate AI cures (click button or use API)
curl -X POST http://localhost:3000/api/analyze-clusters
```

---

## Common Commands

```bash
# Run the server (development)
go run main.go

# Build and run binary
go build -o aiops-dashboard . && ./aiops-dashboard

# Start Ollama (required for AI features)
ollama serve
ollama pull llama3.2

# Static analysis
go vet ./...
```

**Access Dashboard:** http://localhost:3000/

---

## Project Structure

```
Predictive-Analysis-Report/
├── main.go                    # Go backend server (Fiber)
├── index.html                 # Frontend dashboard (vanilla JS)
├── go.mod / go.sum            # Go module dependencies
├── server                     # Compiled binary (arm64)
├── anomaly_report.csv         # Legacy CSV data (deprecated)
├── esx-SGRL-ESX01-*/          # VMware ESXi diagnostic bundle
│   ├── var/run/log/hostd.log  # Primary log source (55k lines)
│   └── vmfs/volumes/*/        # VM-specific logs
└── CLAUDE.md                  # This file
```

---

## Architecture Overview

### Data Flow

```
VMware Logs (55k lines)
        │
        ▼
┌───────────────────┐
│  Regex Parser     │  ← Extracts: timestamp, component, severity, message
│  (main.go)        │
└───────────────────┘
        │
        ▼
┌───────────────────┐
│  Clustering       │  ← Groups similar errors into patterns
│  Algorithm        │     (prevents 55k LLM calls)
└───────────────────┘
        │
        ▼
┌───────────────────┐
│  Ollama LLM       │  ← Analyzes unique patterns only
│  (llama3.2)       │     Returns: Root Cause, Fix, Downtime
└───────────────────┘
        │
        ▼
┌───────────────────┐
│  Dashboard        │  ← Risk scores, charts, AI recommendations
│  (index.html)     │
└───────────────────┘
```

### API Endpoints

| Route | Method | Purpose |
|-------|--------|---------|
| `/` | GET | Serves dashboard |
| `/api/health` | GET | Liveness check (returns anomaly count, patterns, ingestion time) |
| `/api/anomalies` | GET | Paginated anomaly data (params: `page`, `limit`) |
| `/api/stats` | GET | KPIs + timeline data + severity distribution |
| `/api/clusters` | GET | Top 100 unique error patterns by frequency |
| `/api/analyze` | POST | Single error analysis via Ollama |
| `/api/analyze-clusters` | POST | **Batch AI analysis** - generates cures for top 20 patterns |
| `/api/cures` | GET | Cached AI recommendations (diagnosis, prescription, prognosis) |
| `/api/predictions` | GET | 6-hour risk forecast |
| `/ws` | WebSocket | Real-time anomaly stream

### Risk Score Mapping

| Severity | Log Code | Risk Score |
|----------|----------|------------|
| Error/Fatal | E, F | 90 |
| Warning | W | 50 |
| Info | I | 10 |

---

## AI Integration (Ollama)

- **URL:** `http://localhost:11434/api/generate`
- **Model:** `llama3.2` (fallback: `llama3.1:8b`)
- **Timeout:** 30 seconds
- **Fallback:** Static analysis returned if Ollama is unavailable

### AI Response Format ("Dr. System" Persona)

```
🩺 DIAGNOSIS (non-technical explanation)
💊 PRESCRIPTION (technical fix - CLI commands/config changes)
🔮 PROGNOSIS (risk if ignored + estimated downtime)
```

---

## Important Notes

1. **In-Memory Storage:** All data lives in memory; no database persistence
2. **No Authentication:** All endpoints are public (development mode)
3. **CORS Enabled:** Frontend can call API from same origin
4. **Real-Time Simulation:** WebSocket broadcasts anomalies every 5 seconds
5. **Timeline Downsampling:** Stats endpoint limits to 500 data points for performance
6. **Privacy Scrubbing:** IP addresses and usernames are redacted in the UI

---

## Implementation Status

### Completed Features

1. **High-Speed Ingestion** ✅
   - Parses 85,082 log entries from 19 files in ~614ms
   - Supports both `hostd.log` and `vmware.log` formats
   - Regex-based extraction with automatic severity mapping

2. **Clustering Algorithm** ✅
   - Message normalization (removes UUIDs, paths, IPs, PIDs)
   - MD5-based pattern hashing for deduplication
   - Reduced 85k logs to 33,562 unique patterns (2.5x compression)

3. **AI "Cure" Engine** ✅
   - `POST /api/analyze-clusters` - Batch analyzes top 20 critical/warning patterns
   - `GET /api/cures` - Returns cached AI recommendations
   - Structured output: Diagnosis, Prescription (CLI commands), Prognosis, Impact
   - Fallback responses when Ollama is offline

4. **Dashboard** ✅
   - Dark mode with glassmorphism styling
   - KPI cards showing: Total, Critical, Warning, Patterns, Cures
   - Timeline chart and severity distribution
   - "Cure Center" with copy-paste CLI commands
   - Paginated anomaly table with per-row diagnosis

---

## Development Guidelines

- **Modify data via the clustering algorithm**, not individual log entries
- **Test Ollama availability** before relying on AI features
- **Use regex patterns** in `loadAllLogs()` to adapt to different log formats
- **Keep frontend vanilla** - no npm, no build steps
