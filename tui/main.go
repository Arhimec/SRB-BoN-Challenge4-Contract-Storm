package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func clearScreen() {
	fmt.Print("\033[H\033[2J")
}

func progressBar(current, total int, length int) string {
	if current > total {
		current = total
	}
	filled := int((float64(current) / float64(total)) * float64(length))
	empty := length - filled
    if empty < 0 {
		empty = 0
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", empty)
	return bar
}

func main() {
	// Hide cursor
	fmt.Print("\033[?25l")
	defer fmt.Print("\033[?25h")

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		fmt.Print("\033[?25h")
		clearScreen()
		os.Exit(0)
	}()

	for {
		resp, err := http.Get("http://localhost:2112/metrics")
		if err != nil {
			clearScreen()
			fmt.Println("\033[1;33mWaiting for Bot Metrics Server to come online at :2112...\033[0m")
			time.Sleep(1 * time.Second)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		metrics := map[string]int{
			"blindSync":       0,
			"blindAsyncV1":    0,
			"blindAsyncV2":    0,
			"blindTransfExec": 0,
		}
		errors := 0

		lines := strings.Split(string(body), "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "bot_txs_sent_total") {
				for key := range metrics {
					if strings.Contains(line, `call_type="`+key+`"`) {
						parts := strings.Split(line, " ")
						if len(parts) == 2 {
							val, _ := strconv.Atoi(parts[1])
							metrics[key] += val
						}
					}
				}
			} else if strings.HasPrefix(line, "bot_txs_error_total") {
				parts := strings.Split(line, " ")
				if len(parts) == 2 {
					val, _ := strconv.Atoi(parts[1])
					errors += val
				}
			}
		}

		clearScreen()
		fmt.Println("\033[36m=========================================================\033[0m")
		fmt.Println("\033[1;33m  🏆 BATTLE OF NODES - CHALLENGE 4 TUI DASHBOARD 🏆\033[0m")
		fmt.Println("\033[36m=========================================================\033[0m")
		fmt.Println("  \033[1;32m🟢 STATUS: ONLINE\033[0m      \033[1;34m📡 PROXY: BARE-METAL (7950)\033[0m")
		fmt.Println("\033[36m=========================================================\033[0m")
		fmt.Println()

		order := []string{"blindSync", "blindAsyncV1", "blindAsyncV2", "blindTransfExec"}
		for _, key := range order {
			val := metrics[key]
			check := "  "
			if val >= 300 {
				check = "✅"
			}
            
            color := "\033[32m" // default green
            if val < 300 {
                color = "\033[33m" // yellow if not complete
            }

			bar := progressBar(val, 300, 20)
			fmt.Printf("  %-16s %s[%s]\033[0m %4d / 300 %s\n\n", key, color, bar, val, check)
		}

		fmt.Println("\033[36m---------------------------------------------------------\033[0m")
		errColor := "\033[32m"
		if errors > 0 {
			errColor = "\033[31m"
		}
		fmt.Printf("  🛑 %sErrors Detected: %d\033[0m\n", errColor, errors)
		fmt.Println("\033[36m=========================================================\033[0m")
		fmt.Println("  Press Ctrl+C to exit dashboard view")

		time.Sleep(1 * time.Second)
	}
}
