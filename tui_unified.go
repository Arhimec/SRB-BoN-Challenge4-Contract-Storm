package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
	"strconv"
)

type PromResp struct {
	Data struct {
		Result []struct {
			Metric map[string]string `json:"metric"`
			Value  []interface{}     `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

func getMetric(query string) float64 {
	resp, err := http.Get("http://localhost:9090/api/v1/query?query=" + query)
	if err != nil { return 0 }
	defer resp.Body.Close()
	var r PromResp
	json.NewDecoder(resp.Body).Decode(&r)
	if len(r.Data.Result) > 0 && len(r.Data.Result[0].Value) > 1 {
		val, _ := strconv.ParseFloat(r.Data.Result[0].Value[1].(string), 64)
		return val
	}
	return 0
}

func getMetricMap(query string) map[string]float64 {
	resp, err := http.Get("http://localhost:9090/api/v1/query?query=" + query)
	if err != nil { return nil }
	defer resp.Body.Close()
	var r PromResp
	json.NewDecoder(resp.Body).Decode(&r)
	res := make(map[string]float64)
	for _, item := range r.Data.Result {
		if len(item.Value) > 1 {
			val, _ := strconv.ParseFloat(item.Value[1].(string), 64)
			res[item.Metric["ct"]] = val
		}
	}
	return res
}

func progressBar(val, max float64, width int, color string) string {
	if val > max { val = max }
	percent := (val / max) * float64(width)
	filled := int(percent)
	bar := ""
	for i := 0; i < width; i++ {
		if i < filled {
			bar += color + "█\033[0m"
		} else {
			bar += "░"
		}
	}
	return bar
}

const (
	ColorGreen  = "\033[32m"
	ColorYellow = "\033[33m"
	ColorBlue   = "\033[34m"
	ColorCyan   = "\033[36m"
	ColorRed    = "\033[31m"
	ColorBold   = "\033[1m"
	ColorReset  = "\033[0m"
)

func main() {
	fmt.Print("\033[H\033[2J")
	types := []string{"blindSync", "blindAsyncV1", "blindAsyncV2", "blindTransfExec"}
	for {
		tps := getMetric("sum(irate(bot_txs_sent_total[1m]))")
		total := getMetric("sum(bot_txs_sent_total)")
		typeCounts := getMetricMap("sum by (ct) (bot_txs_sent_total)")

		fmt.Printf("\033[H")
		fmt.Printf("%s====================================================%s\n", ColorBlue, ColorReset)
		fmt.Printf("%s    %sSUPER RARE BEARS - FLEET COMMAND DASHBOARD%s%s       \n", ColorCyan, ColorBold, ColorReset, ColorCyan)
		fmt.Printf("%s====================================================%s\n", ColorBlue, ColorReset)
		
		fmt.Printf(" [FLEET TPS]   :  %s%s%.2f tx/s%s\n", ColorBold, ColorGreen, tps, ColorReset)
		fmt.Printf(" [TOTAL TXS]   :  %s%s%.0f / 2500%s  %s\n", ColorBold, ColorYellow, total, ColorReset, progressBar(total, 2500, 15, ColorYellow))
		
		fmt.Println("\n QUALIFIER PROGRESS (300 MIN/TYPE):")
		fmt.Println(" ----------------------------------------------------")
		for _, ct := range types {
			count := typeCounts[ct]
			fmt.Printf(" %-16s: %4.0f / 300  %s\n", ct, count, progressBar(count, 300, 20, ColorGreen))
		}
		
		fmt.Printf("\n%s [FLEET SIZE]:  99 Nodes across 3 Shards%s\n", ColorBold, ColorReset)
		fmt.Printf("%s Prometheus  :  Online (173.249.39.152)%s\n", ColorGreen, ColorReset)
		fmt.Printf("====================================================\n")
		fmt.Printf(" Last Updated : %s\n", time.Now().Format("15:04:05"))
		time.Sleep(2 * time.Second)
	}
}
