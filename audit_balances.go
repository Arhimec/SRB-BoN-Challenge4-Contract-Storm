package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"strconv"
)

func main() {
	data, _ := os.ReadFile("all_fleet_addresses.txt")
	lines := strings.Split(string(data), "\n")
	proxy := "https://gateway.battleofnodes.com"
	for _, l := range lines {
		addr := strings.TrimSpace(l)
		if addr == "" || addr == "erd10mcwua04j5r9ujny9y3pza32kmeeq9vqs8eq2je26r27yh28ynfqhxmahk" { continue }
		resp, err := http.Get(proxy + "/address/" + addr)
		if err != nil { continue }
		var r struct { Data struct { Account struct { Balance string } } }
		json.NewDecoder(resp.Body).Decode(&r)
		bal, _ := strconv.ParseFloat(r.Data.Account.Balance, 64)
		if bal > 0 {
			fmt.Printf("%s: %s\n", addr, r.Data.Account.Balance)
		}
		resp.Body.Close()
	}
}
