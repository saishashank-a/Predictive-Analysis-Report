package main

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
)

// ========================================
// DATA STRUCTURES
// ========================================

// Anomaly represents a single log entry
type Anomaly struct {
	Timestamp   time.Time `json:"timestamp"`
	Component   string    `json:"component"`
	Subsystem   string    `json:"subsystem"`
	Message     string    `json:"message"`
	RiskScore   int       `json:"risk_score"`
	Severity    string    `json:"severity"`
	PatternHash string    `json:"pattern_hash"` // Links to cluster
}

// PatternCluster represents a group of similar errors
type PatternCluster struct {
	PatternHash      string  `json:"pattern_hash"`
	CanonicalMessage string  `json:"canonical_message"` // Representative message
	Component        string  `json:"component"`
	Subsystem        string  `json:"subsystem"`
	Severity         string  `json:"severity"`
	Count            int     `json:"count"`
	FirstSeen        string  `json:"first_seen"`
	LastSeen         string  `json:"last_seen"`
	RiskScore        int     `json:"risk_score"`
	Cure             *AICure `json:"cure,omitempty"` // AI-generated fix
}

// AICure represents the AI-generated diagnosis and fix
type AICure struct {
	Diagnosis       string `json:"diagnosis"`        // "Like I'm 5" explanation
	Prescription    string `json:"prescription"`     // CLI command or fix
	Prognosis       string `json:"prognosis"`        // Risk if ignored
	EstimatedImpact string `json:"estimated_impact"` // Downtime estimation
	Source          string `json:"source"`           // "ollama" or "fallback"
	GeneratedAt     string `json:"generated_at"`
}

// Client represents a connected WebSocket user
type Client struct {
	Conn *websocket.Conn
}

// Global Hub Variables
var (
	clients    = make(map[*Client]bool)
	broadcast  = make(chan Anomaly)
	register   = make(chan *Client)
	unregister = make(chan *Client)
	mutex      = &sync.Mutex{}
)

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

type PaginatedResponse struct {
	Total      int       `json:"total"`
	Page       int       `json:"page"`
	Limit      int       `json:"limit"`
	TotalPages int       `json:"total_pages"`
	Data       []Anomaly `json:"data"`
}

type Stats struct {
	TotalCount       int            `json:"total_count"`
	CriticalCount    int            `json:"critical_count"`
	WarningCount     int            `json:"warning_count"`
	AvgRisk          int            `json:"avg_risk"`
	ComponentData    map[string]int `json:"component_data"`
	TimelineData     []TimePoint    `json:"timeline_data"`
	UniquePatterns   int            `json:"unique_patterns"`
	PatternsAnalyzed int            `json:"patterns_analyzed"`
	IngestionTimeMs  int64          `json:"ingestion_time_ms"`
	SeverityData     map[string]int `json:"severity_data"`
}

type TimePoint struct {
	Timestamp time.Time `json:"x"`
	RiskScore int       `json:"y"`
}

type Prediction struct {
	Timestamp     time.Time `json:"timestamp"`
	PredictedRisk float64   `json:"predicted_risk"`
	LowerBound    float64   `json:"lower_bound"`
	UpperBound    float64   `json:"upper_bound"`
}

type AnalyzeRequest struct {
	ErrorMsg string `json:"error_msg"`
}

type ChatRequest struct {
	Message string `json:"message"`
	Context string `json:"context,omitempty"` // System context (e.g., error log)
}

type ChatResponse struct {
	Response string `json:"response"`
}

type OllamaChatRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type OllamaChatResponse struct {
	Message ChatMessage `json:"message"`
}

// ========================================
// GLOBALS: In-Memory Data Store
// ========================================

var (
	anomalies       []Anomaly
	patternClusters = make(map[string]*PatternCluster) // patternHash -> cluster
	ingestionTimeMs int64
	filesProcessed  int
	linesProcessed  int
)

// ========================================
// CONFIGURATION
// ========================================

const (
	RootDir         = "esx-SGRL-ESX01-2026-01-10--09.39-2102690"
	Port            = ":3000"
	OllamaURL       = "http://localhost:11434/api/generate"
	OllamaModel     = "llama3.1"
	OllamaTimeout   = 60 * time.Second
	ChatHistoryFile = "chat_history.json"
)

// Simple Chat History Storage
type SavedChat struct {
	Timestamp string `json:"timestamp"`
	Context   string `json:"context"`
	Question  string `json:"question"`
	Response  string `json:"response"`
}

var chatHistory []SavedChat

// ========================================
// MAIN ENTRY POINT
// ========================================

func main() {
	log.Println("🚀 Starting AIOps Dashboard - The Self-Healing Engine")

	// 1. High-Speed Data Ingestion with benchmarking
	start := time.Now()
	loadAllLogs()
	ingestionTimeMs = time.Since(start).Milliseconds()

	// Load Chat History
	loadChatHistory()

	log.Printf("⚡ BENCHMARK: Ingested %d log entries from %d files in %dms",
		len(anomalies), filesProcessed, ingestionTimeMs)
	log.Printf("📊 Identified %d unique error patterns (clusters)", len(patternClusters))

	// Start WebSocket Hub
	go runHub()

	// 2. Setup Fiber Server
	app := fiber.New(fiber.Config{
		AppName: "AIOps Self-Healing Dashboard v2.0",
	})

	// Enable CORS
	app.Use(cors.New())

	// ========================================
	// ROUTES
	// ========================================

	// Serve frontend
	app.Get("/", func(c *fiber.Ctx) error {
		return c.SendFile("./index.html")
	})

	// Health check
	app.Get("/api/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"status":            "healthy",
			"anomalies":         len(anomalies),
			"patterns":          len(patternClusters),
			"ingestion_time_ms": ingestionTimeMs,
		})
	})

	// Core Data Endpoints
	app.Get("/api/anomalies", handleGetAnomalies)
	app.Get("/api/stats", handleGetStats)
	app.Get("/api/clusters", handleGetClusters)
	app.Get("/api/predictions", handlePredictions)

	// AI "Cure" Endpoints
	app.Post("/api/analyze", handleAnalyzeSingle)            // Single error analysis
	app.Post("/api/analyze-clusters", handleAnalyzeClusters) // Batch analyze all clusters
	app.Post("/api/analyze-clusters", handleAnalyzeClusters) // Batch analyze all clusters
	app.Get("/api/cures", handleGetCures)                    // Get all cached cures
	app.Post("/api/chat", handleChat)                        // Interactive Chat Endpoint

	// WebSocket
	app.Use("/ws", func(c *fiber.Ctx) error {
		if websocket.IsWebSocketUpgrade(c) {
			return c.Next()
		}
		return fiber.ErrUpgradeRequired
	})
	app.Get("/ws", websocket.New(handleWebSocket))

	// Start Real-Time Simulation
	go simulateRealTimeTraffic()

	// 3. Start Server
	log.Printf("🏥 Dashboard ready at http://localhost%s", Port)
	log.Printf("💊 AI Cure Engine: POST /api/analyze-clusters to generate fixes")
	log.Fatal(app.Listen(Port))
}

// ========================================
// HIGH-SPEED LOG INGESTION
// ========================================

func loadAllLogs() {
	// Regex patterns for different log formats

	// hostd.log format: 2025-12-09T04:04:13.227Z warning hostd[2099429] [Originator@6876 sub=Vmsvc...] Message
	hostdRegex := regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z)\s+(info|warning|error|verbose|trivia)\s+(\w+)\[\d+\]\s+\[.*?sub=([^\s\]]+).*?\]\s+(.*)$`)

	// vmware.log format: 2025-12-27T02:23:54.263Z| vmx| I125: Message
	vmwareRegex := regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z)\|\s*(\S+)\|\s*([A-Z])\d*:\s*(.*)$`)

	// Pattern normalization regex - removes variable parts
	pathRegex := regexp.MustCompile(`/vmfs/volumes/[a-f0-9-]+/[^/]+/[^'"\s]+`)
	uuidRegex := regexp.MustCompile(`[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}`)
	pidRegex := regexp.MustCompile(`\[\d+\]`)
	ipRegex := regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)
	hexRegex := regexp.MustCompile(`0x[a-fA-F0-9]+`)
	numberRegex := regexp.MustCompile(`\b\d{4,}\b`) // Large numbers (task IDs, etc)

	normalizeMessage := func(msg string) string {
		normalized := msg
		normalized = pathRegex.ReplaceAllString(normalized, "[PATH]")
		normalized = uuidRegex.ReplaceAllString(normalized, "[UUID]")
		normalized = pidRegex.ReplaceAllString(normalized, "[PID]")
		normalized = ipRegex.ReplaceAllString(normalized, "[IP]")
		normalized = hexRegex.ReplaceAllString(normalized, "[HEX]")
		normalized = numberRegex.ReplaceAllString(normalized, "[NUM]")
		return normalized
	}

	hashPattern := func(component, subsystem, severity, normalizedMsg string) string {
		data := fmt.Sprintf("%s|%s|%s|%s", component, subsystem, severity, normalizedMsg)
		hash := md5.Sum([]byte(data))
		return hex.EncodeToString(hash[:])[:12] // Short hash
	}

	// Walk all files
	err := filepath.Walk(RootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}
		if info.IsDir() {
			return nil
		}

		// Process hostd.log
		if info.Name() == "hostd.log" {
			filesProcessed++
			processLogFile(path, hostdRegex, "hostd", normalizeMessage, hashPattern)
		}

		// Process vmware.log files
		if info.Name() == "vmware.log" {
			filesProcessed++
			processVMwareLog(path, vmwareRegex, normalizeMessage, hashPattern)
		}

		return nil
	})

	if err != nil {
		log.Printf("⚠️ Error walking directory: %v", err)
	}

	// Sort anomalies by timestamp (newest first)
	sort.Slice(anomalies, func(i, j int) bool {
		return anomalies[i].Timestamp.After(anomalies[j].Timestamp)
	})

	if len(anomalies) == 0 {
		log.Println("⚠️ No logs found, generating mock data...")
		generateMockData()
	}
}

func processLogFile(path string, re *regexp.Regexp, defaultComponent string,
	normalizeMessage func(string) string,
	hashPattern func(string, string, string, string) string) {

	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	// Increase buffer size for long lines
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		linesProcessed++

		matches := re.FindStringSubmatch(line)
		if len(matches) >= 5 {
			tsStr := matches[1]
			severityRaw := strings.ToLower(matches[2])
			component := matches[3]
			subsystem := matches[4]
			message := matches[5]

			// Parse timestamp
			ts, err := time.Parse("2006-01-02T15:04:05.000Z", tsStr)
			if err != nil {
				continue
			}

			// Map severity to risk score
			severity := mapSeverity(severityRaw)
			riskScore := severityToRisk(severity)

			// Create pattern hash for clustering
			normalized := normalizeMessage(message)
			patternHash := hashPattern(component, subsystem, severity, normalized)

			// Add anomaly
			anomaly := Anomaly{
				Timestamp:   ts,
				Component:   component,
				Subsystem:   subsystem,
				Message:     message,
				RiskScore:   riskScore,
				Severity:    severity,
				PatternHash: patternHash,
			}
			anomalies = append(anomalies, anomaly)

			// Update cluster
			updateCluster(patternHash, anomaly, normalized)
		}
	}
}

func processVMwareLog(path string, re *regexp.Regexp,
	normalizeMessage func(string) string,
	hashPattern func(string, string, string, string) string) {

	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	// Extract VM name from path
	parts := strings.Split(path, "/")
	vmName := "unknown"
	for i, p := range parts {
		if p == "vmfs" && i+3 < len(parts) {
			vmName = parts[i+3]
			break
		}
	}

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		linesProcessed++

		matches := re.FindStringSubmatch(line)
		if len(matches) >= 5 {
			tsStr := matches[1]
			component := matches[2]
			severityCode := matches[3]
			message := matches[4]

			ts, err := time.Parse("2006-01-02T15:04:05.000Z", tsStr)
			if err != nil {
				continue
			}

			severity := vmwareSeverityMap(severityCode)
			riskScore := severityToRisk(severity)

			normalized := normalizeMessage(message)
			patternHash := hashPattern(component, vmName, severity, normalized)

			anomaly := Anomaly{
				Timestamp:   ts,
				Component:   component,
				Subsystem:   vmName,
				Message:     message,
				RiskScore:   riskScore,
				Severity:    severity,
				PatternHash: patternHash,
			}
			anomalies = append(anomalies, anomaly)

			updateCluster(patternHash, anomaly, normalized)
		}
	}
}

func updateCluster(patternHash string, anomaly Anomaly, canonicalMsg string) {
	mutex.Lock()
	defer mutex.Unlock()

	if cluster, exists := patternClusters[patternHash]; exists {
		cluster.Count++
		if anomaly.Timestamp.Format(time.RFC3339) > cluster.LastSeen {
			cluster.LastSeen = anomaly.Timestamp.Format(time.RFC3339)
		}
		if anomaly.Timestamp.Format(time.RFC3339) < cluster.FirstSeen {
			cluster.FirstSeen = anomaly.Timestamp.Format(time.RFC3339)
		}
	} else {
		patternClusters[patternHash] = &PatternCluster{
			PatternHash:      patternHash,
			CanonicalMessage: truncateMessage(canonicalMsg, 200),
			Component:        anomaly.Component,
			Subsystem:        anomaly.Subsystem,
			Severity:         anomaly.Severity,
			Count:            1,
			FirstSeen:        anomaly.Timestamp.Format(time.RFC3339),
			LastSeen:         anomaly.Timestamp.Format(time.RFC3339),
			RiskScore:        anomaly.RiskScore,
		}
	}
}

func mapSeverity(raw string) string {
	switch raw {
	case "error":
		return "Critical"
	case "warning":
		return "Warning"
	case "info", "verbose", "trivia":
		return "Info"
	default:
		return "Info"
	}
}

func vmwareSeverityMap(code string) string {
	switch code {
	case "E", "F":
		return "Critical"
	case "W":
		return "Warning"
	default:
		return "Info"
	}
}

func severityToRisk(severity string) int {
	switch severity {
	case "Critical":
		return 90
	case "Warning":
		return 50
	default:
		return 10
	}
}

func truncateMessage(msg string, maxLen int) string {
	if len(msg) > maxLen {
		return msg[:maxLen] + "..."
	}
	return msg
}

func generateMockData() {
	components := []string{"hostd", "vmx", "vpxa", "vmkernel"}
	subsystems := []string{"Vmsvc", "Default", "Libs", "TaskManager"}
	severities := []string{"Critical", "Warning", "Info"}

	now := time.Now()
	for i := 0; i < 100; i++ {
		severity := severities[i%len(severities)]
		anomalies = append(anomalies, Anomaly{
			Timestamp:   now.Add(time.Duration(-i*15) * time.Minute),
			Component:   components[i%len(components)],
			Subsystem:   subsystems[i%len(subsystems)],
			Message:     fmt.Sprintf("Mock error %d - System anomaly detected", i),
			RiskScore:   severityToRisk(severity),
			Severity:    severity,
			PatternHash: fmt.Sprintf("mock-%d", i%10),
		})
	}
}

// ========================================
// API HANDLERS
// ========================================

func handleGetAnomalies(c *fiber.Ctx) error {
	page := c.QueryInt("page", 1)
	limit := c.QueryInt("limit", 50)

	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}

	total := len(anomalies)
	start := (page - 1) * limit
	end := start + limit

	if start > total {
		start = total
	}
	if end > total {
		end = total
	}

	return c.JSON(PaginatedResponse{
		Total:      total,
		Page:       page,
		Limit:      limit,
		TotalPages: (total + limit - 1) / limit,
		Data:       anomalies[start:end],
	})
}

func handleGetStats(c *fiber.Ctx) error {
	total := len(anomalies)
	if total == 0 {
		return c.JSON(Stats{})
	}

	critical := 0
	warning := 0
	sumRisk := 0
	compCounts := make(map[string]int)
	severityCounts := make(map[string]int)
	timeline := []TimePoint{}

	// Downsample for timeline
	step := 1
	if total > 500 {
		step = total / 500
	}

	for i, a := range anomalies {
		if a.Severity == "Critical" {
			critical++
		} else if a.Severity == "Warning" {
			warning++
		}
		sumRisk += a.RiskScore
		compCounts[a.Component]++
		severityCounts[a.Severity]++

		if i%step == 0 {
			timeline = append(timeline, TimePoint{
				Timestamp: a.Timestamp,
				RiskScore: a.RiskScore,
			})
		}
	}

	// Count patterns with cures
	patternsAnalyzed := 0
	for _, cluster := range patternClusters {
		if cluster.Cure != nil {
			patternsAnalyzed++
		}
	}

	return c.JSON(Stats{
		TotalCount:       total,
		CriticalCount:    critical,
		WarningCount:     warning,
		AvgRisk:          sumRisk / total,
		ComponentData:    compCounts,
		TimelineData:     timeline,
		UniquePatterns:   len(patternClusters),
		PatternsAnalyzed: patternsAnalyzed,
		IngestionTimeMs:  ingestionTimeMs,
		SeverityData:     severityCounts,
	})
}

func handleGetClusters(c *fiber.Ctx) error {
	// Convert map to sorted slice
	clusters := make([]*PatternCluster, 0, len(patternClusters))
	for _, cluster := range patternClusters {
		clusters = append(clusters, cluster)
	}

	// Sort by count descending (most frequent first)
	sort.Slice(clusters, func(i, j int) bool {
		return clusters[i].Count > clusters[j].Count
	})

	// Limit to top 100 clusters
	if len(clusters) > 100 {
		clusters = clusters[:100]
	}

	return c.JSON(clusters)
}

func handleGetCures(c *fiber.Ctx) error {
	// Return all clusters that have cures
	curedClusters := make([]*PatternCluster, 0)
	for _, cluster := range patternClusters {
		if cluster.Cure != nil {
			curedClusters = append(curedClusters, cluster)
		}
	}

	// Sort by count
	sort.Slice(curedClusters, func(i, j int) bool {
		return curedClusters[i].Count > curedClusters[j].Count
	})

	return c.JSON(curedClusters)
}

// ========================================
// AI "CURE" ENGINE
// ========================================

func handleAnalyzeSingle(c *fiber.Ctx) error {
	var req AnalyzeRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid request"})
	}

	response, source := analyzeWithOllama(req.ErrorMsg)

	return c.JSON(fiber.Map{
		"response": response,
		"source":   source,
	})
}

func handleAnalyzeClusters(c *fiber.Ctx) error {
	// Get critical and warning clusters first
	clustersToAnalyze := make([]*PatternCluster, 0)
	for _, cluster := range patternClusters {
		if cluster.Cure == nil && (cluster.Severity == "Critical" || cluster.Severity == "Warning") {
			clustersToAnalyze = append(clustersToAnalyze, cluster)
		}
	}

	// Sort by count (most impactful first)
	sort.Slice(clustersToAnalyze, func(i, j int) bool {
		return clustersToAnalyze[i].Count > clustersToAnalyze[j].Count
	})

	// Limit to top 20 to prevent overloading Ollama
	maxAnalyze := 20
	if len(clustersToAnalyze) > maxAnalyze {
		clustersToAnalyze = clustersToAnalyze[:maxAnalyze]
	}

	analyzed := 0
	failed := 0

	for _, cluster := range clustersToAnalyze {
		log.Printf("🔬 Analyzing cluster %s (%d occurrences): %s",
			cluster.PatternHash, cluster.Count, truncateMessage(cluster.CanonicalMessage, 50))

		cure := generateCure(cluster)
		cluster.Cure = cure

		if cure.Source == "ollama" {
			analyzed++
		} else {
			failed++
		}

		// Small delay to not overwhelm Ollama
		time.Sleep(500 * time.Millisecond)
	}

	return c.JSON(fiber.Map{
		"status":         "complete",
		"analyzed":       analyzed,
		"failed":         failed,
		"total_clusters": len(patternClusters),
		"message":        fmt.Sprintf("Analyzed %d clusters with AI, %d used fallback", analyzed, failed),
	})
}

func generateCure(cluster *PatternCluster) *AICure {
	prompt := fmt.Sprintf(`You are "Dr. System", an expert VMware ESXi diagnostician. Analyze this error pattern and provide actionable fixes.

ERROR PATTERN:
- Component: %s
- Subsystem: %s
- Severity: %s
- Occurrences: %d
- Sample Error: "%s"

Provide your response in EXACTLY this format (use these exact headers):

🩺 DIAGNOSIS:
[Explain what this error means in simple terms a manager could understand. 2-3 sentences max.]

💊 PRESCRIPTION:
[Provide 1-2 specific CLI commands or configuration changes to fix this. Be precise and copy-paste ready.]

🔮 PROGNOSIS:
[What happens if ignored? Include estimated downtime risk. 1-2 sentences.]

⏱️ ESTIMATED IMPACT:
[Low/Medium/High impact + estimated resolution time]`,
		cluster.Component,
		cluster.Subsystem,
		cluster.Severity,
		cluster.Count,
		cluster.CanonicalMessage)

	response, source := analyzeWithOllama(prompt)

	// Load Chat History
	// This part of the code seems to be misplaced based on the instruction "Add functions after main loop."
	// Assuming the user intended to add these functions at the file level and the `loadChatHistory()` call
	// was meant to be in a main-like function, but since `main` is not provided, I'm placing the functions
	// at the end of the file and removing the misplaced call.

	cure := &AICure{
		Source:      source,
		GeneratedAt: time.Now().Format(time.RFC3339),
	}

	if source == "ollama" {
		// Parse sections from response
		cure.Diagnosis = extractSection(response, "🩺 DIAGNOSIS:", "💊")
		cure.Prescription = extractSection(response, "💊 PRESCRIPTION:", "🔮")
		cure.Prognosis = extractSection(response, "🔮 PROGNOSIS:", "⏱️")
		cure.EstimatedImpact = extractSection(response, "⏱️ ESTIMATED IMPACT:", "")

		// Fallback if parsing failed
		if cure.Diagnosis == "" {
			cure.Diagnosis = response
		}
	} else {
		// Generate fallback cure based on error type
		cure = generateFallbackCure(cluster)
	}

	return cure
}

func extractSection(text, startMarker, endMarker string) string {
	startIdx := strings.Index(text, startMarker)
	if startIdx == -1 {
		return ""
	}
	startIdx += len(startMarker)

	endIdx := len(text)
	if endMarker != "" {
		if idx := strings.Index(text[startIdx:], endMarker); idx != -1 {
			endIdx = startIdx + idx
		}
	}

	return strings.TrimSpace(text[startIdx:endIdx])
}

// ========================================
// PERSISTENCE HELPERS
// ========================================

func loadChatHistory() {
	file, err := os.ReadFile(ChatHistoryFile)
	if err == nil {
		json.Unmarshal(file, &chatHistory)
		log.Printf("📜 Loaded %d chat history items", len(chatHistory))
	}
}

func saveChatHistory() {
	data, _ := json.MarshalIndent(chatHistory, "", "  ")
	os.WriteFile(ChatHistoryFile, data, 0644)
}

// ========================================
// INTERACTIVE CHAT HANDLER
// ========================================

func handleChat(c *fiber.Ctx) error {
	var req ChatRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid request"})
	}

	// Construct conversation for Ollama
	messages := []ChatMessage{}

	// System Context
	systemPrompt := `You are Dr. System, an intelligent, patient, and highly informative AIOps assistant. 
Your goal is to help users diagnose infrastructure issues with clarity and depth. 

**Response Guidelines:**
1. **Tone:** Maintain a calm, professional, and patient tone.
2. **Structure:** Do NOT use single block paragraphs. Use clear headers (###), bullet points, and ample line spacing.
3. **Detail:** Provide comprehensive explanations. Analyze the error, explain potential causes, and offer step-by-step solutions.
4. **Context:** Use the provided error logs to give specific, not generic, advice.
`
	if req.Context != "" {
		systemPrompt += fmt.Sprintf("\n\nCURRENT CONTEXT:\n%s", req.Context)

		// RAG: Check history for similar context
		for _, item := range chatHistory {
			if strings.Contains(item.Context, req.Context) { // Simple exact match for now
				systemPrompt += fmt.Sprintf("\n\nMEMORY RECALL - PREVIOUS SIMILAR ISSUE:\nOne user previously asked: %s\nThe solution was: %s\nUse this to inform your answer.", item.Question, item.Response)
				break
			}
		}
	}
	messages = append(messages, ChatMessage{Role: "system", Content: systemPrompt})

	// User Message
	messages = append(messages, ChatMessage{Role: "user", Content: req.Message})

	ollamaReq := OllamaChatRequest{
		Model:    OllamaModel,
		Messages: messages,
		Stream:   false,
	}

	reqBody, _ := json.Marshal(ollamaReq)
	client := &http.Client{Timeout: 60 * time.Second}

	// Use Ollama Chat API
	chatURL := "http://localhost:11434/api/chat"
	resp, err := client.Post(chatURL, "application/json", bytes.NewBuffer(reqBody))

	if err != nil || resp.StatusCode != 200 {
		log.Printf("⚠️ Chat API failed: %v", err)
		return c.JSON(ChatResponse{Response: "I'm having trouble connecting to my brain (Ollama). Please ensure 'llama3.2' is installed."})
	}
	defer resp.Body.Close()

	var ollamaResp OllamaChatResponse
	json.NewDecoder(resp.Body).Decode(&ollamaResp)

	aiResponse := ollamaResp.Message.Content

	// Save to History (if context was present)
	if req.Context != "" {
		chatHistory = append(chatHistory, SavedChat{
			Timestamp: time.Now().Format(time.RFC3339),
			Context:   req.Context,
			Question:  req.Message,
			Response:  aiResponse,
		})
		// Keep history manageable (last 100)
		if len(chatHistory) > 100 {
			chatHistory = chatHistory[len(chatHistory)-100:]
		}
		saveChatHistory()
	}

	return c.JSON(ChatResponse{Response: aiResponse})
}

func analyzeWithOllama(prompt string) (string, string) {
	ollamaReq := OllamaRequest{
		Model:  OllamaModel,
		Prompt: prompt,
		Stream: false,
	}

	reqBody, err := json.Marshal(ollamaReq)
	if err != nil {
		return generateFallbackResponse(prompt), "fallback"
	}

	client := &http.Client{Timeout: OllamaTimeout}
	resp, err := client.Post(OllamaURL, "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		log.Printf("⚠️ Ollama unavailable: %v", err)
		return generateFallbackResponse(prompt), "fallback"
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("⚠️ Ollama returned status %d. Hint: Run 'ollama pull %s'", resp.StatusCode, OllamaModel)
		return generateFallbackResponse(prompt), "fallback"
	}

	var ollamaResp OllamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return generateFallbackResponse(prompt), "fallback"
	}

	return ollamaResp.Response, "ollama"
}

// generateFallbackResponse creates a helpful response when Ollama is unavailable
func generateFallbackResponse(errorMsg string) string {
	msg := strings.ToLower(errorMsg)

	var diagnosis, prescription, prognosis string

	switch {
	case strings.Contains(msg, "operation not supported"):
		diagnosis = "The system is attempting an operation that isn't available on this storage configuration. This is typically a compatibility issue between vCenter features and the underlying storage."
		prescription = "esxcli storage vmfs extent list\nvim-cmd vimsvc/license --show"
		prognosis = "Low risk - informational warning. May affect some advanced features but won't cause downtime."

	case strings.Contains(msg, "error_file_not_found") || strings.Contains(msg, "queryinformation"):
		diagnosis = "The host is checking for domain join configuration but finding none. This is normal for standalone ESXi hosts not joined to Active Directory."
		prescription = "No action required if AD integration is not needed."
		prognosis = "No risk - This is informational noise. Can be safely ignored."

	case strings.Contains(msg, "task created") || strings.Contains(msg, "task completed"):
		diagnosis = "Normal vCenter-to-ESXi task management communication. These are routine sync operations."
		prescription = "No action needed - healthy heartbeat operations."
		prognosis = "No risk - This indicates healthy vCenter communication."

	case strings.Contains(msg, "congestion"):
		diagnosis = "Network stack configuration message showing the TCP congestion control algorithm in use."
		prescription = "esxcli network ip netstack set --netstack=defaultTcpipStack --congestion-control-algorithm=cubic"
		prognosis = "No risk - Informational message about network configuration."

	case strings.Contains(msg, "warning") || strings.Contains(msg, "warn"):
		diagnosis = "This is a warning-level event that indicates a potential issue that hasn't caused a failure yet."
		prescription = "tail -100 /var/log/hostd.log | grep -i warning\nvim-cmd vmsvc/getallvms"
		prognosis = "Medium risk - Monitor for escalation to errors."

	case strings.Contains(msg, "error") || strings.Contains(msg, "fail"):
		diagnosis = "An error condition has been detected. This requires attention to prevent potential service disruption."
		prescription = "tail -500 /var/log/hostd.log | grep -i error\nesxcli system syslog mark --message='Investigating error'"
		prognosis = "High risk - Investigate promptly to prevent cascading failures."

	default:
		diagnosis = "This log entry requires analysis. Review the context and surrounding log entries for more information."
		prescription = "tail -100 /var/log/hostd.log\nvim-cmd hostsvc/hostsummary"
		prognosis = "Risk level depends on frequency and context. Monitor for patterns."
	}

	return fmt.Sprintf(`**🩺 DIAGNOSIS**
%s

**💊 PRESCRIPTION**
%s

**🔮 PROGNOSIS**
%s`, diagnosis, prescription, prognosis)
}

func generateFallbackCure(cluster *PatternCluster) *AICure {
	cure := &AICure{
		Source:      "fallback",
		GeneratedAt: time.Now().Format(time.RFC3339),
	}

	// Generate context-aware fallback based on error patterns
	msg := strings.ToLower(cluster.CanonicalMessage)

	switch {
	case strings.Contains(msg, "operation not supported"):
		cure.Diagnosis = "The system is attempting an operation that isn't available on this storage configuration. This is typically a compatibility issue between vCenter features and the underlying storage."
		cure.Prescription = "1. Check storage compatibility: esxcli storage vmfs extent list\n2. Verify vCenter version matches ESXi: vim-cmd vimsvc/license --show"
		cure.Prognosis = "Low risk - informational warning. May affect some advanced features but won't cause downtime."
		cure.EstimatedImpact = "Low impact - No immediate action required"

	case strings.Contains(msg, "error_file_not_found") || strings.Contains(msg, "queryinformation"):
		cure.Diagnosis = "The host is checking for domain join configuration but finding none. This is normal for standalone ESXi hosts not joined to Active Directory."
		cure.Prescription = "1. If AD integration desired: esxcli system account add --id=admin --role=Admin\n2. To suppress: Edit /etc/vmware/hostd/config.xml and disable AD checks"
		cure.Prognosis = "No risk - This is informational noise. Can be safely ignored if AD integration is not required."
		cure.EstimatedImpact = "Low impact - 0 minutes downtime"

	case strings.Contains(msg, "task created") || strings.Contains(msg, "task completed"):
		cure.Diagnosis = "Normal vCenter-to-ESXi task management communication. These are routine sync operations between vCenter and the host."
		cure.Prescription = "No action needed. These are healthy heartbeat operations."
		cure.Prognosis = "No risk - This indicates healthy vCenter communication."
		cure.EstimatedImpact = "No impact - Normal operation"

	case strings.Contains(msg, "congestion control"):
		cure.Diagnosis = "Network stack configuration message showing the TCP congestion control algorithm in use. This is a normal startup/config message."
		cure.Prescription = "To change algorithm if needed: esxcli network ip netstack set --netstack=defaultTcpipStack --congestion-control-algorithm=cubic"
		cure.Prognosis = "No risk - Informational message about network configuration."
		cure.EstimatedImpact = "No impact - Configuration notice"

	default:
		cure.Diagnosis = fmt.Sprintf("This %s-level event in the %s subsystem requires investigation. The pattern has occurred %d times.",
			cluster.Severity, cluster.Subsystem, cluster.Count)
		cure.Prescription = fmt.Sprintf("1. Check component logs: tail -100 /var/log/%s.log\n2. Review VMware KB articles for: %s",
			strings.ToLower(cluster.Component), truncateMessage(cluster.CanonicalMessage, 50))
		cure.Prognosis = "Risk depends on frequency. Monitor for escalation."
		cure.EstimatedImpact = fmt.Sprintf("%s impact - Requires analysis", cluster.Severity)
	}

	return cure
}

// ========================================
// PREDICTIONS
// ========================================

func handlePredictions(c *fiber.Ctx) error {
	if len(anomalies) == 0 {
		return c.JSON([]Prediction{})
	}

	// Calculate trend from recent anomalies
	windowSize := 100
	if len(anomalies) < windowSize {
		windowSize = len(anomalies)
	}
	recent := anomalies[:windowSize]

	var sumRisk float64
	for _, a := range recent {
		sumRisk += float64(a.RiskScore)
	}
	avgRisk := sumRisk / float64(len(recent))

	// Project 6 hours forward
	var predictions []Prediction
	lastTime := time.Now()

	for i := 1; i <= 6; i++ {
		drift := (rand.Float64() - 0.5) * 10
		predicted := avgRisk + drift

		if predicted < 0 {
			predicted = 0
		}
		if predicted > 100 {
			predicted = 100
		}

		futureTime := lastTime.Add(time.Duration(i) * time.Hour)

		predictions = append(predictions, Prediction{
			Timestamp:     futureTime,
			PredictedRisk: predicted,
			LowerBound:    predicted - 5,
			UpperBound:    predicted + 5,
		})
	}

	return c.JSON(predictions)
}

// ========================================
// WEBSOCKET
// ========================================

func runHub() {
	for {
		select {
		case client := <-register:
			mutex.Lock()
			clients[client] = true
			mutex.Unlock()

		case client := <-unregister:
			mutex.Lock()
			if _, ok := clients[client]; ok {
				delete(clients, client)
				client.Conn.Close()
			}
			mutex.Unlock()

		case message := <-broadcast:
			mutex.Lock()
			for client := range clients {
				payload, _ := json.Marshal(message)
				if err := client.Conn.WriteMessage(websocket.TextMessage, payload); err != nil {
					client.Conn.Close()
					delete(clients, client)
				}
			}
			mutex.Unlock()
		}
	}
}

func handleWebSocket(c *websocket.Conn) {
	client := &Client{Conn: c}
	register <- client

	defer func() {
		unregister <- client
	}()

	for {
		_, _, err := c.ReadMessage()
		if err != nil {
			break
		}
	}
}

func simulateRealTimeTraffic() {
	time.Sleep(2 * time.Second)

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		if len(anomalies) > 0 {
			randIdx := rand.Intn(len(anomalies))
			anomaly := anomalies[randIdx]
			anomaly.Timestamp = time.Now()
			broadcast <- anomaly
		}
	}
}
