# 🚀 AIOps Dashboard - Quick Start Guide

## 1. AI Setup (Ollama)

The dashboard relies on **Ollama** for the "Dr. System" chat and cure generation.

**Step 1: Start Ollama Server**
Open a terminal and run:

```bash
ollama serve
```

**Step 2: Pull the AI Model**
In a new terminal, download the model (only needs to be done once):

```bash
ollama pull llama3.1
```

*Note: We are using `llama3.1` by default. If you change this, update `OllamaModel` in `main.go`.*

---

## 2. Run the Dashboard

**Step 1: Navigate to Project Folder**

```bash
cd /Users/saishashankanchuri/Documents/---work---/Predictive-Analysis-Report
```

**Step 2: Start the Server**

```bash
go run main.go
```

**Step 3: Access**
Open your browser and go to:
👉 **<http://localhost:3000>**

---

## 3. Common Troubleshooting

**Issue: Port 3000 already in use**
If `go run main.go` fails, find and kill the old process:

```bash
# Find the Process ID (PID)
lsof -i :3000

# Kill the process (replace <PID> with the actual number)
kill -9 <PID>
```

**Issue: "Unable to reach Dr. System"**

- Check if Ollama is running (`ollama serve`).
- Verify the model is installed (`ollama list`).
