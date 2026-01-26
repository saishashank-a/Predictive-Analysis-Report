package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
)

// -- Data Structures --

type LogEvent struct {
	Timestamp time.Time
	Component string
	Severity  string
	Message   string
}

// TimeBin represents a 5-minute window of system activity
type TimeBin struct {
	StartTime   string `json:"start_time"`   // For chart Labels
	TotalErrors int    `json:"total_errors"` // Height of bar
	IsAnomaly   bool   `json:"is_anomaly"`   // Color of bar (Red/Green)
	MainCause   string `json:"main_cause"`   // Top component driving errors
}

// Stats holds our aggregations
type SystemStats struct {
	TotalEvents      int            `json:"total_events"`
	FailureProb      float64        `json:"failure_probability"` // 0-100%
	PredictedNextVal float64        `json:"predicted_next_val"`  // Linear Regression Result
	DeathSpiralStage int            `json:"death_spiral_stage"`  // 0, 1, 2, 3
	TopErrors        []string       `json:"top_errors"`
	Timeline         []TimeBin      `json:"timeline"`
	ComponentCounts  map[string]int `json:"component_counts"`
}

// OllamaRequest for sending prompts to Ollama
type OllamaRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
}

// OllamaResponse for receiving analysis from Ollama
type OllamaResponse struct {
	Response string `json:"response"`
}

var (
	rawLogs []LogEvent
	stats   SystemStats
)

const (
	LogFile = "esx-SGRL-ESX01-2026-01-10--09.39-2102690/var/run/log/vmkernel.log"
	Port    = ":3000"

	OllamaAPIEndpoint = "http://localhost:11434/api/generate"
	OllamaModel       = "llama3" // Or "mistral", "phi3", etc.
)

func main() {
	// 1. Ingest & Feature Engineering
	processLogData()

	// 2. Setup Server
	app := fiber.New()
	app.Use(cors.New())

	// API: Get the high-level stats & timeline (Lightweight)
	app.Get("/api/stats", func(c *fiber.Ctx) error {
		return c.JSON(stats)
	})

	// API: Trigger SRE Analysis (Ollama)
	app.Post("/api/analyze", handleAIAnalysis)

	// API: Explain Specific Log (Ollama)
	app.Post("/api/explain", handleLogExplanation)

	app.Get("/api/health", func(c *fiber.Ctx) error { return c.SendStatus(200) })
	app.Get("/", func(c *fiber.Ctx) error { return c.SendFile("./index.html") })

	log.Printf("🚀 Predictive Engine running on http://localhost%s", Port)
	log.Fatal(app.Listen(Port))
}

func processLogData() {
	file, err := os.Open(LogFile)
	if err != nil {
		log.Printf("⚠️  Could not open log: %v", err)
		return
	}
	defer file.Close()

	// Regex for: 2025-12-27T00:20:40.756Z cpu23:2098417)Elf: 2101: ...
	re := regexp.MustCompile(`^(\S+) \S+\)(\w+): (.*)$`)

	var events []LogEvent
	compCounts := make(map[string]int)

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		matches := re.FindStringSubmatch(line)
		if len(matches) == 4 {
			t, _ := time.Parse(time.RFC3339, matches[1])
			comp := matches[2]
			msg := matches[3]

			// Determine Severity
			sev := "INFO"
			msgLower := strings.ToLower(msg)
			if strings.Contains(msgLower, "failed") || strings.Contains(msgLower, "error") {
				sev = "ERROR"
			} else if strings.Contains(msgLower, "warning") {
				sev = "WARN"
			}

			events = append(events, LogEvent{
				Timestamp: t,
				Component: comp,
				Severity:  sev,
				Message:   msg,
			})
			compCounts[comp]++
		}
	}
	rawLogs = events
	log.Printf("✅ Parsed %d raw events", len(rawLogs))

	// -- Feature Engineering: 5-Minute Bins --
	if len(events) == 0 {
		return
	}

	// Group by 5-min window
	bins := make(map[int64]*TimeBin) // Key: Unix timestamp / 300

	// Track stats for anomaly detection
	var totalErrCount float64
	var binKeys []int64

	for _, e := range events {
		// Round down to nearest 5 min (300 sec)
		key := e.Timestamp.Unix() / 300

		if _, exists := bins[key]; !exists {
			bins[key] = &TimeBin{StartTime: e.Timestamp.Format("15:04")}
			binKeys = append(binKeys, key)
		}

		if e.Severity == "ERROR" || e.Severity == "WARN" {
			bins[key].TotalErrors++
			bins[key].MainCause = e.Component // Simplified "Top Cause" logic
		}
	}

	// Calculate Stats for Anomaly Detection (Mean + 2*StdDev)
	for _, k := range binKeys {
		totalErrCount += float64(bins[k].TotalErrors)
	}
	mean := totalErrCount / float64(len(binKeys))

	// Prepare Final Timeline (Sorted)
	sort.Slice(binKeys, func(i, j int) bool { return binKeys[i] < binKeys[j] })

	var timeline []TimeBin
	for _, k := range binKeys {
		b := bins[k]
		// Anomaly Logic: Is this bin spikier than average?
		// Simple threshold for now: > Mean * 1.5
		if float64(b.TotalErrors) > (mean * 1.5) {
			b.IsAnomaly = true
		}
		timeline = append(timeline, *b)
	}

	// -- Final Stats Package --
	// Calculate Top 5 "Recursive" Errors
	msgCounts := make(map[string]int)
	msgComp := make(map[string]string)
	for _, e := range events {
		shortMsg := e.Message
		if len(shortMsg) > 50 {
			shortMsg = shortMsg[:50] + "..."
		} // Truncate for display
		msgCounts[shortMsg]++
		msgComp[shortMsg] = e.Component
	}

	// Sort by Frequency
	type ErrEntry struct {
		Msg   string
		Count int
	}
	var sortedErrs []ErrEntry
	for k, v := range msgCounts {
		sortedErrs = append(sortedErrs, ErrEntry{k, v})
	}
	sort.Slice(sortedErrs, func(i, j int) bool { return sortedErrs[i].Count > sortedErrs[j].Count })

	// Pick Top 5
	var top5 []string
	for i := 0; i < 5 && i < len(sortedErrs); i++ {
		// Format: "105|ScsiDeviceIO|Cmd failed due to timeout"
		entry := fmt.Sprintf("%d|%s|%s", sortedErrs[i].Count, msgComp[sortedErrs[i].Msg], sortedErrs[i].Msg)
		top5 = append(top5, entry)
	}

	stats = SystemStats{
		TotalEvents:     len(events),
		Timeline:        timeline,
		ComponentCounts: compCounts,
		TopErrors:       top5,
		FailureProb:     0.1,
	}

	// 1. Calculate Linear Regression Trend
	stats.PredictedNextVal = predictTrend(timeline)

	// 2. Detect Death Spiral Stage
	stats.DeathSpiralStage, stats.FailureProb = detectDeathSpiral(compCounts, timeline)

	log.Printf("📊 Generated %d time-bins. Mean: %.2f, Predicted Next: %.2f", len(timeline), mean, stats.PredictedNextVal)
}

// Linear Regression: y = mx + b
func predictTrend(data []TimeBin) float64 {
	if len(data) < 2 {
		return 0
	}

	// Take last 12 points (1 hour) for trend
	n := len(data)
	if n > 12 {
		data = data[n-12:]
		n = 12
	}

	var sumX, sumY, sumXY, sumX2 float64
	for i, d := range data {
		x := float64(i)
		y := float64(d.TotalErrors)
		sumX += x
		sumY += y
		sumXY += x * y
		sumX2 += x * x
	}

	// Slope (m)
	m := (float64(n)*sumXY - sumX*sumY) / (float64(n)*sumX2 - sumX*sumX)
	// Intercept (b)
	b := (sumY - m*sumX) / float64(n)

	// Predict next point (x = n)
	next := m*float64(n) + b
	if next < 0 {
		return 0
	}
	return next
}

// Heuristic: Check for "Death Spiral" Patterns
func detectDeathSpiral(counts map[string]int, timeline []TimeBin) (int, float64) {
	stage := 0
	prob := 10.0

	// Phase 1: Management Blindness (Hostd/Vpxa flakey)
	if counts["hostd"] > 5 || counts["vpxa"] > 5 {
		stage = 1
		prob = 35.0
	}

	// Phase 2: IO Choke (SCSI Latency / Storage)
	// If we are already in Stage 1 OR have massive SCSI errors
	if (stage == 1 && counts["ScsiDeviceIO"] > 10) || counts["ScsiDeviceIO"] > 50 {
		stage = 2
		prob = 65.0
	}

	// Phase 3: Terminal (Memory Exhaustion / Fatal)
	// Check for specific keywords in recent bins
	for i := len(timeline) - 1; i >= 0 && i > len(timeline)-6; i-- {
		// If recent bins are Anomaly AND we have accumulated memory errors
		if timeline[i].IsAnomaly && (counts["UserMem"] > 0 || counts["VisorFSRam"] > 0) {
			stage = 3
			prob = 98.5
			break
		}
	}

	return stage, prob
}

// -- AI Integration --

func handleAIAnalysis(c *fiber.Ctx) error {
	// Construct the SRE Prompt
	prompt := fmt.Sprintf(`
Role: Principal Site Reliability Engineer (SRE).
Context: Analyzing VMware ESXi logs for imminent failure.
Data:
- Total Events: %d
- Top Failing Component: %s
- Prediction: "Death Spiral" Stage %d (Prob: %.1f%%)
- Top Recurring Errors: %v

Task: Write a concise "Predictive Infrastructure Health Report".
1. Executive Summary: One-liner on current state.
2. Root Cause Analysis: Interpret the top errors (e.g. Memory affecting Storage).
3. Predicted Downtime: Estimate when the system might crash if untreated.
4. Next Best Action: Specific remediation (vMotion, Reboot, etc).

Output Format: Plain text, professional tone, no markdown.
`, stats.TotalEvents, "Unknown", stats.DeathSpiralStage, stats.FailureProb, stats.TopErrors)

	reqBody, _ := json.Marshal(OllamaRequest{
		Model:  "llama3.1",
		Prompt: prompt,
		Stream: false,
	})

	// Call Ollama
	resp, err := http.Post("http://localhost:11434/api/generate", "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		return c.Status(503).SendString("Ollama not running: " + err.Error())
	}
	defer resp.Body.Close()

	var result OllamaResponse
	json.NewDecoder(resp.Body).Decode(&result)

	return c.JSON(result)
}

// -- Single Log Explanation --

type LogExplanationRequest struct {
	Component string `json:"component"`
	Message   string `json:"message"`
}

func handleLogExplanation(c *fiber.Ctx) error {
	var body LogExplanationRequest
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).SendString(err.Error())
	}

	prompt := fmt.Sprintf(`
Role: VMware Expert SRE.
Log Component: %s
Log Message: "%s"

Task:
1. Simplify: Explain what this error means in plain English.
2. Fix: Suggest 1 concrete command or action to resolve it.

Output Format:
**Explanation:** ...
**Suggested Fix:** ...
`, body.Component, body.Message)

	reqBody, _ := json.Marshal(OllamaRequest{
		Model:  "llama3.1",
		Prompt: prompt,
		Stream: false,
	})

	resp, err := http.Post("http://localhost:11434/api/generate", "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		return c.Status(503).SendString("Ollama error: " + err.Error())
	}
	defer resp.Body.Close()

	var result OllamaResponse
	json.NewDecoder(resp.Body).Decode(&result)
	return c.JSON(result)
}
