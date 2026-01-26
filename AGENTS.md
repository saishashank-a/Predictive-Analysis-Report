# AGENTS.md

This file provides guidance to WARP (warp.dev) when working with code in this repository.

## Development commands

This repository is a small Go web service (using Fiber) that serves a single-page HTML dashboard backed by data from `anomaly_report.csv`.

### Prerequisites
- Go toolchain installed (version compatible with the `go` directive in `go.mod`).

### Run the service in development
- From the repository root:
  - Run the server directly:
    - `go run main.go`
  - Build and run a local binary:
    - `go build -o aiops-dashboard .`
    - `./aiops-dashboard`
- The server listens on port `3000` (see `Port` constant in `main.go`) and serves the dashboard at:
  - `http://localhost:3000/`

The server expects `anomaly_report.csv` to be present in the working directory (same directory as `main.go`). If the CSV cannot be opened, the service still starts but exposes an empty dataset and logs a warning.

### Testing
There are currently no Go test files in this repository, but standard Go test commands apply if tests are added later:
- Run all tests:
  - `go test ./...`
- Run a single test (by name pattern):
  - `go test ./... -run TestName`

### Basic static analysis
No linting configuration is checked into this repo. You can still use the built-in Go tooling:
- `go vet ./...` – run basic static analysis on the module.

## High-level architecture

### Overview

This project is a minimal full-stack dashboard built as:
- A Go HTTP API using [Fiber](https://github.com/gofiber/fiber) that:
  - Loads anomaly data from a CSV file into memory on startup.
  - Exposes JSON APIs for that data.
  - Serves a static HTML/JS dashboard.
- A single static HTML file (`index.html`) that:
  - Calls the backend API to fetch anomaly data.
  - Renders KPIs, charts, and a table using vanilla JavaScript and ApexCharts.
  - Applies basic client-side scrubbing of sensitive information in log messages.

### Backend service (`main.go`)

Key concepts:
- **Data model**
  - `Anomaly` struct: `timestamp`, `component`, `message`, `risk_score` (int).
  - A global slice `anomalies []Anomaly` acts as an in-memory cache of all records loaded from `anomaly_report.csv`.

- **Data loading lifecycle** (`loadData`):
  - On process startup, `loadData()` opens `CSVFile` (`anomaly_report.csv`).
  - It skips the header row and iterates the remaining lines.
  - For each row, it parses the risk score as an integer and appends an `Anomaly` instance to the global `anomalies` slice.
  - Errors while reading individual records are skipped rather than failing the whole load.
  - If the CSV is missing or cannot be opened, a warning is logged and the function returns without populating data.

- **HTTP server & routes** (Fiber):
  - `app := fiber.New()` creates the Fiber app; CORS is enabled globally via `app.Use(cors.New())` so the dashboard can call the API from the same origin.
  - Routes:
    - `GET /api/health` – simple liveness check returning HTTP `200` on success.
    - `GET /` – serves `index.html` from the repo root via `c.SendFile("./index.html")`.
    - `GET /api/anomalies` – returns the full `anomalies` slice as JSON. This is the primary data source for the dashboard.
  - The server logs a startup message and listens on `:3000` (constant `Port`).

This backend is intentionally stateful in memory and reads from CSV only once at startup; there is no persistence layer beyond the CSV file and no authentication/authorization.

### Frontend dashboard (`index.html`)

Key responsibilities:
- **Styling & layout**
  - Pure HTML/CSS (no frontend framework) with a glassmorphism-inspired theme and a light/dark mode controlled via CSS variables and a `data-theme` attribute on the `<html>` element.

- **State management**
  - A simple `appState` object in JavaScript tracks:
    - `data`: array of anomaly records fetched from `/api/anomalies`.
    - `theme`: `'dark'` or `'light'`, persisted to `localStorage` and applied on page load.

- **Data fetching and rendering**
  - `init()` fetches `/api/anomalies`, stores the result in `appState.data`, then calls `renderDashboard()`.
  - `renderDashboard()` is the central render function:
    - Computes KPIs: total events, count of critical events (`risk_score > 80`), and average risk score.
    - Renders two ApexCharts charts:
      - Area chart over time (risk score vs timestamp) in `#timeline-chart`.
      - Donut chart of counts per `component` in `#pie-chart`.
    - Recreates charts on each render to pick up theme changes.
    - Renders the main anomaly table (`#anomaly-table tbody`) from `appState.data`.

- **Privacy scrubbing**
  - `scrubData(text)` performs client-side redaction before displaying log messages:
    - Replaces IPv4 addresses with `[REDACTED IP]`.
    - Redacts `User: <name>` patterns to `User: [REDACTED]`.
  - The table rendering path calls `scrubData(d.message)` to avoid rendering raw sensitive data to the UI.

- **Theme toggling**
  - `toggleTheme()` flips between `dark` and `light` themes, stores the preference in `localStorage`, updates the `data-theme` attribute, and re-renders charts to reflect the new theme.

- **AI analysis placeholder**
  - `analyzeError(msg)` opens an "AI Doctor" modal (`#ai-modal`) and logs the message to the console. This is a stub for a future AI-backed diagnosis feature; no network calls are made yet.

### Data flow summary

1. The Go backend starts, reads anomaly data from `anomaly_report.csv` into memory, and exposes it via `/api/anomalies`.
2. The browser loads `index.html` from `/` and runs the embedded JavaScript.
3. The frontend calls `/api/anomalies`, receives the JSON representation of the `anomalies` slice, and stores it in `appState.data`.
4. The dashboard renders KPIs, charts, and a scrubbed table view of the anomalies, updating automatically when the theme changes.
