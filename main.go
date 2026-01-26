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
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
)

// ========================================
// DATA STRUCTURES
// ========================================

// Anomaly represents a single row from anomaly_report.csv
type Anomaly struct {
	Timestamp time.Time `json:"timestamp"`
	Component string    `json:"component"`
	Message   string    `json:"message"`
	RiskScore int       `json:"risk_score"`
}

// AnalyzeRequest for the AI analysis endpoint
type AnalyzeRequest struct {
	ErrorMsg string `json:"error_msg"`
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

// ========================================
// GLOBALS: In-Memory Data Store (O(1) access)
// ========================================

var anomalies []Anomaly

// ========================================
// CONFIGURATION
// ========================================

const (
	LogFilePath   = "esx-SGRL-ESX01-2026-01-10--09.39-2102690/var/run/log/hostd.log"
	Port          = ":3000"
	OllamaURL     = "http://localhost:11434/api/generate"
	OllamaModel   = "llama3.2"
	OllamaTimeout = 30 * time.Second
)

// ========================================
// MAIN ENTRY POINT
// ========================================

func main() {
	// 1. Data Ingestion: Load Logs into memory on startup
	loadAnomalies()

	// 2. Setup Fiber Server
	app := fiber.New(fiber.Config{
		AppName: "AIOps Dashboard v1.0",
	})

	// Enable CORS for development
	app.Use(cors.New())

	// ========================================
	// ROUTES
	// ========================================

	// Serve the frontend
	app.Get("/", func(c *fiber.Ctx) error {
		return c.SendFile("./index.html")
	})

	// API: Return all anomalies as JSON (Phase 1 Deliverable)
	app.Get("/api/anomalies", func(c *fiber.Ctx) error {
		return c.JSON(anomalies)
	})

	// Health check for load balancer
	app.Get("/api/health", func(c *fiber.Ctx) error {
		return c.SendString("OK")
	})

	// AI Proxy: Forward to local Ollama instance (Phase 3)
	app.Post("/api/analyze", handleAnalyze)

	// ========================================
	// START SERVER
	// ========================================

	log.Printf("🚀 AIOps Dashboard running at http://localhost%s", Port)
	log.Printf("📊 Loaded %d log entries from %s", len(anomalies), LogFilePath)
	log.Fatal(app.Listen(Port))
}

// ========================================
// DATA INGESTION LOGIC
// ========================================

// loadAnomalies reads the VMware hostd.log file into the in-memory slice
func loadAnomalies() {
	file, err := os.Open(LogFilePath)
	if err != nil {
		log.Printf("⚠️  Log file not found (%s). Generating mock data...", LogFilePath)
		generateMockData()
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)

	// Regex for VMware hostd logs
	// Example: 2025-12-09T04:04:13.227Z warning hostd[2099429] [Originator@6876 sub=Vmsvc.vm:/vmfs/volumes/... user=vpxuser] Message...
	// Group 1: Timestamp
	// Group 2: Severity
	// Group 3: Process Info (hostd[...])
	// Group 4: Metadata (Originator...)
	// Group 5: Message
	re := regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z) (\w+) (\S+) \[(.*?)\] (.*)$`)

	count := 0
	for scanner.Scan() {
		line := scanner.Text()
		matches := re.FindStringSubmatch(line)

		if len(matches) < 6 {
			continue // Skip malformed lines
		}

		tsStr := matches[1]
		severity := matches[2]
		// process := matches[3]
		metadata := matches[4]
		message := matches[5]

		// Parse timestamp
		ts, err := time.Parse(time.RFC3339, tsStr)
		if err != nil {
			ts = time.Now()
		}

		// Calculate Risk Score
		riskScore := 10 // Default Info
		switch severity {
		case "error":
			riskScore = 90
		case "warning":
			riskScore = 50
		case "verbose":
			riskScore = 5
		}

		// Extract cleaner component from metadata (e.g., "sub=Vmsvc.vm" -> "Vmsvc.vm")
		component := "System"
		if strings.Contains(metadata, "sub=") {
			parts := strings.Split(metadata, " ")
			for _, p := range parts {
				if strings.HasPrefix(p, "sub=") {
					component = strings.TrimPrefix(p, "sub=")
					break
				}
			}
		}

		anomalies = append(anomalies, Anomaly{
			Timestamp: ts,
			Component: component,
			Message:   message,
			RiskScore: riskScore,
		})
		count++
	}

	if err := scanner.Err(); err != nil {
		log.Printf("⚠️  Error reading log file: %v", err)
	}

	log.Printf("✅ Loaded %d log entries from %s", count, LogFilePath)
}

// generateMockData creates sample anomalies if Log file is missing
func generateMockData() {
	components := []string{"Network", "Database", "Auth Service", "Storage", "API Gateway", "Payment Service"}
	messages := []string{
		"Connection timeout from 10.0.0.5",
		"High latency detected on /api/v1/users",
		"Invalid token signature User: admin",
		"Disk usage above 90% on /mnt/data",
		"Certificate expiration warning",
		"Transaction processing delay",
	}

	now := time.Now()
	for i := 0; i < 20; i++ {
		anomalies = append(anomalies, Anomaly{
			Timestamp: now.Add(time.Duration(-i*15) * time.Minute),
			Component: components[i%len(components)],
			Message:   messages[i%len(messages)],
			RiskScore: 30 + (i*5)%70,
		})
	}
	log.Printf("✅ Generated %d mock anomalies", len(anomalies))
}

// ========================================
// AI INTEGRATION LOGIC (Ollama Reverse Proxy)
// ========================================

// handleAnalyze acts as a REVERSE PROXY to the local Ollama instance.
//
// PROXY PATTERN:
// 1. Frontend calls POST /api/analyze (avoids CORS issues)
// 2. Go backend constructs the "System Doctor" prompt
// 3. Go backend forwards request to Ollama (localhost:11434)
// 4. If Ollama is offline/times out, return a static fallback response
// 5. Return AI response (or fallback) to frontend
//
// This pattern is superior to direct browser->Ollama calls because:
// - No CORS configuration needed on Ollama
// - Backend can rate-limit, cache, or log AI requests
// - Backend provides graceful fallback if AI is unavailable
func handleAnalyze(c *fiber.Ctx) error {
	var req AnalyzeRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid request body"})
	}

	// Construct the "System Doctor" prompt
	prompt := fmt.Sprintf(`You are "Dr. System", a friendly and knowledgeable System Doctor who diagnoses infrastructure ailments.

A patient (system component) is showing the following symptoms:
"%s"

Please provide:

🩺 **DIAGNOSIS** (What's happening, explained simply for a non-technical manager)
Explain what this error means in plain, comforting terms. Use a medical analogy if helpful.

💊 **PRESCRIPTION** (Technical remedy for the engineer)
Provide 1-2 specific, actionable fixes the ops team can implement immediately.

🔮 **PROGNOSIS** (What happens if untreated)
Briefly describe the risk of ignoring this issue.

Keep your response concise but thorough. Be helpful and reassuring.`, req.ErrorMsg)

	// Create Ollama request
	ollamaReq := OllamaRequest{
		Model:  OllamaModel,
		Prompt: prompt,
		Stream: false,
	}

	reqBody, err := json.Marshal(ollamaReq)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to create request"})
	}

	// Call Ollama with timeout
	client := &http.Client{Timeout: OllamaTimeout}
	resp, err := client.Post(OllamaURL, "application/json", bytes.NewBuffer(reqBody))

	// FAIL-SAFE: If Ollama is offline, return static analysis
	if err != nil {
		log.Printf("⚠️  Ollama unavailable: %v", err)
		return c.JSON(fiber.Map{
			"response": generateFallbackAnalysis(req.ErrorMsg),
			"source":   "fallback",
		})
	}
	defer resp.Body.Close()

	// Check for non-200 response
	if resp.StatusCode != http.StatusOK {
		log.Printf("⚠️  Ollama returned status %d", resp.StatusCode)
		return c.JSON(fiber.Map{
			"response": generateFallbackAnalysis(req.ErrorMsg),
			"source":   "fallback",
		})
	}

	// Parse Ollama response
	var ollamaResp OllamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		log.Printf("⚠️  Failed to decode Ollama response: %v", err)
		return c.JSON(fiber.Map{
			"response": generateFallbackAnalysis(req.ErrorMsg),
			"source":   "fallback",
		})
	}

	return c.JSON(fiber.Map{
		"response": ollamaResp.Response,
		"source":   "ollama",
	})
}

// generateFallbackAnalysis provides a static response when Ollama is unavailable
func generateFallbackAnalysis(errorMsg string) string {
	return fmt.Sprintf(`🩺 **DIAGNOSIS**
The system is experiencing an infrastructure anomaly. The logged error indicates a potential service degradation that should be investigated promptly.

💊 **PRESCRIPTION**
1. Check the affected component's logs for more context
2. Verify network connectivity and resource availability
3. Consider restarting the affected service if the issue persists

🔮 **PROGNOSIS**
If left unaddressed, this issue could escalate to service disruption affecting dependent systems.

---
⚠️ *Note: AI analysis is currently offline. This is a static fallback response.*
Error analyzed: "%s"`, errorMsg)
}
